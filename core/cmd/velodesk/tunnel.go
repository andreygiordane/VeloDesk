// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Túneis TCP: o controlador abre uma porta local que leva ao computador controlado (ex.: RDP, banco de dados).
// Os bytes viajam por um canal WebRTC criptografado; aqui ficam só as pontas TCP e a política de destino.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

func tokenOK(r *http.Request, tok string) bool {
	got := r.Header.Get("X-Agent-Token")
	if got == "" {
		got = r.URL.Query().Get("t")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1
}

// destAllowed: só loopback e redes privadas (LAN). Nunca a internet aberta.
func destAllowed(host string) bool {
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else if r, err := net.LookupIP(host); err == nil {
		ips = r
	}
	if len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
			return false
		}
	}
	return true
}

var tunnelActive atomic.Int32

func bridgeWS(ctx context.Context, c *websocket.Conn, tc net.Conn) {
	tunnelActive.Add(1)
	defer tunnelActive.Add(-1)
	c.SetReadLimit(1 << 20)
	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := tc.Read(buf)
			if n > 0 && c.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
				break
			}
			if err != nil {
				break
			}
		}
		done <- struct{}{}
	}()
	go func() {
		for {
			typ, b, err := c.Read(ctx)
			if err != nil {
				break
			}
			if typ == websocket.MessageBinary {
				if _, err := tc.Write(b); err != nil {
					break
				}
			}
		}
		done <- struct{}{}
	}()
	<-done
	tc.Close()
	c.CloseNow()
}

func registerTunnels(mux *http.ServeMux, guard func(http.HandlerFunc) http.HandlerFunc,
	decode func(http.ResponseWriter, *http.Request, any) bool, jsonOut func(http.ResponseWriter, any), agentTok string) {
	var mu sync.Mutex
	listeners := map[int]net.Listener{}
	pending := map[string]net.Conn{}
	var ctl *websocket.Conn
	var ctlCtx context.Context

	notify := func(m map[string]any) bool {
		mu.Lock()
		c, ctx := ctl, ctlCtx
		mu.Unlock()
		if c == nil {
			return false
		}
		b, _ := json.Marshal(m)
		return c.Write(ctx, websocket.MessageText, b) == nil
	}

	mux.HandleFunc("/api/tunnel/listen", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Port int }
		if !tokenOK(r, agentTok) || r.Method != http.MethodPost || !decode(w, r, &in) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if in.Port < 1024 || in.Port > 65535 {
			http.Error(w, "Use uma porta local entre 1024 e 65535", http.StatusBadRequest)
			return
		}
		mu.Lock()
		if _, ok := listeners[in.Port]; ok || len(listeners) >= 16 {
			mu.Unlock()
			http.Error(w, "Porta já está em uso por outro túnel (ou limite de túneis atingido)", http.StatusConflict)
			return
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(in.Port)))
		if err != nil {
			mu.Unlock()
			http.Error(w, "Não foi possível abrir a porta: "+err.Error(), http.StatusConflict)
			return
		}
		listeners[in.Port] = l
		mu.Unlock()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				if tunnelActive.Load() >= 64 {
					c.Close()
					continue
				}
				raw := make([]byte, 8)
				rand.Read(raw)
				cid := hex.EncodeToString(raw)
				mu.Lock()
				pending[cid] = c
				mu.Unlock()
				time.AfterFunc(15*time.Second, func() {
					mu.Lock()
					p, ok := pending[cid]
					delete(pending, cid)
					mu.Unlock()
					if ok {
						p.Close()
					}
				})
				if !notify(map[string]any{"t": "conn", "cid": cid, "port": in.Port}) {
					mu.Lock()
					delete(pending, cid)
					mu.Unlock()
					c.Close()
				}
			}
		}()
		jsonOut(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/tunnel/close", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Port int }
		if !tokenOK(r, agentTok) || r.Method != http.MethodPost || !decode(w, r, &in) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mu.Lock()
		if l, ok := listeners[in.Port]; ok {
			l.Close()
			delete(listeners, in.Port)
		}
		mu.Unlock()
		jsonOut(w, map[string]bool{"ok": true})
	}))
	// fecha todos os túneis (fim da sessão)
	mux.HandleFunc("/api/tunnel/closeall", guard(func(w http.ResponseWriter, r *http.Request) {
		if !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mu.Lock()
		for p, l := range listeners {
			l.Close()
			delete(listeners, p)
		}
		mu.Unlock()
		jsonOut(w, map[string]bool{"ok": true})
	}))

	// canal de avisos "chegou uma conexão" (core -> página)
	mux.HandleFunc("/tunnel/ctl", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) || !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		mu.Lock()
		if ctl != nil {
			ctl.CloseNow()
		}
		ctl, ctlCtx = c, ctx
		mu.Unlock()
		for {
			if _, _, err := c.Read(ctx); err != nil {
				break
			}
		}
		mu.Lock()
		if ctl == c {
			ctl = nil
		}
		mu.Unlock()
		c.CloseNow()
	})
	// dados de uma conexão aceita localmente (lado do controlador)
	mux.HandleFunc("/tunnel/data", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) || !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		cid := r.URL.Query().Get("cid")
		mu.Lock()
		tc, ok := pending[cid]
		delete(pending, cid)
		mu.Unlock()
		if !ok {
			http.Error(w, "conexão expirada", http.StatusNotFound)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			tc.Close()
			return
		}
		bridgeWS(r.Context(), c, tc)
	})
	// conexão de saída (lado do computador controlado), só para destinos permitidos
	mux.HandleFunc("/tunnel/dial", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) || !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		host, port := r.URL.Query().Get("host"), r.URL.Query().Get("port")
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || !destAllowed(host) || tunnelActive.Load() >= 64 {
			http.Error(w, "destino não permitido", http.StatusForbidden)
			return
		}
		tc, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 5*time.Second)
		if err != nil {
			http.Error(w, fmt.Sprint("não foi possível conectar: ", err), http.StatusBadGateway)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			tc.Close()
			return
		}
		bridgeWS(r.Context(), c, tc)
	})
}
