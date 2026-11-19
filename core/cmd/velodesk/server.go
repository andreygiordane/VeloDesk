// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// VeloDesk signaling server (original code). Run it once on a machine that all
// your devices can reach; the clients (the normal VeloDesk app) register here and
// exchange connection messages. Video/input never pass through it when a direct
// P2P path exists.
//
// Protocol (JSON over WebSocket, /ws):
//
//	client -> server: {"t":"hello","id":"123456789"}           register (id optional; server assigns)
//	server -> client: {"t":"welcome","id":"123456789"}
//	client -> server: {"t":"signal","to":"<id>","data":{...}}  forwarded as-is
//	server -> client: {"t":"signal","from":"<id>","data":{...}}
//	server -> client: {"t":"error","msg":"peer_offline"}

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

var verbose bool

type msg struct {
	T    string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	To   string          `json:"to,omitempty"`
	From string          `json:"from,omitempty"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
	IDs  []string        `json:"ids,omitempty"` // presence: quais IDs conferir
	On   []string        `json:"on,omitempty"`  // presence: os que estão online
}

type peer struct {
	id   string
	send chan msg
}

type hub struct {
	mu    sync.RWMutex
	peers map[string]*peer
}

func newID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900_000_000))
	return fmt.Sprintf("%09d", n.Int64()+100_000_000)
}

func validID(s string) bool {
	if len(s) != 9 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (h *hub) register(want string) *peer {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := want
	if !validID(id) || h.peers[id] != nil {
		for id = newID(); h.peers[id] != nil; id = newID() {
		}
	}
	p := &peer{id: id, send: make(chan msg, 64)}
	h.peers[id] = p
	return p
}

func (h *hub) remove(p *peer) {
	h.mu.Lock()
	if h.peers[p.id] == p {
		delete(h.peers, p.id)
	}
	h.mu.Unlock()
}

func (h *hub) get(id string) *peer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.peers[id]
}

func (h *hub) serveWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 10)
	ctx := r.Context()

	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	var hello msg
	if err := readJSON(hctx, c, &hello); err != nil || hello.T != "hello" {
		cancel()
		c.Close(websocket.StatusPolicyViolation, "hello required")
		return
	}
	cancel()

	p := h.register(hello.ID)
	if verbose {
		log.Printf("peer online %s (asked for %q)", p.id, hello.ID)
	}
	defer h.remove(p)
	p.send <- msg{T: "welcome", ID: p.id}

	go func() {
		for {
			select {
			case m := <-p.send:
				wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				b, _ := json.Marshal(m)
				err := c.Write(wctx, websocket.MessageText, b)
				cancel()
				if err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	var lastPresence time.Time
	for {
		var m msg
		if err := readJSON(ctx, c, &m); err != nil {
			return
		}
		if m.T == "presence" { // a página pergunta quais dos IDs conhecidos estão online
			if time.Since(lastPresence) < 2*time.Second {
				continue
			}
			lastPresence = time.Now()
			var on []string
			for i, id := range m.IDs {
				if i >= 100 {
					break
				}
				if h.get(id) != nil {
					on = append(on, id)
				}
			}
			select {
			case p.send <- msg{T: "presence", On: on}:
			default:
			}
			continue
		}
		if m.T != "signal" {
			continue
		}
		dst := h.get(m.To)
		if verbose {
			var k struct {
				Kind string `json:"kind"`
			}
			json.Unmarshal(m.Data, &k)
			log.Printf("signal %s -> %s kind=%s delivered=%v", p.id, m.To, k.Kind, dst != nil)
		}
		if dst == nil {
			p.send <- msg{T: "error", Msg: "peer_offline", To: m.To}
			continue
		}
		select {
		case dst.send <- msg{T: "signal", From: p.id, Data: m.Data}:
		default:
			p.send <- msg{T: "error", Msg: "peer_busy", To: m.To}
		}
	}
}

func readJSON(ctx context.Context, c *websocket.Conn, v any) error {
	_, b, err := c.Read(ctx)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	addr := fs.String("addr", ":21116", "listen address")
	cert := fs.String("cert", "", "TLS certificate file (enables wss://)")
	key := fs.String("key", "", "TLS key file")
	selfSigned := fs.Bool("tls", false, "wss:// with an auto-generated self-signed certificate (tests)")
	fs.BoolVar(&verbose, "v", false, "log signaling messages")
	updates := fs.String("updates", "", "directory with signed releases to distribute (see `velodesk publish`)")
	fs.Parse(args)

	h := &hub{peers: map[string]*peer{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.serveWS)
	if *updates != "" {
		files := http.FileServer(http.Dir(*updates))
		mux.Handle("/update/", http.StripPrefix("/update/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") || strings.Contains(r.URL.Path, "..") { // no listings
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		})))
		go h.watchUpdates(filepath.Join(*updates, "manifest.json"))
		go h.watchUpdates(filepath.Join(*updates, "manifest-beta.json"))
		log.Printf("distributing updates from %s", *updates)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("VeloDesk signaling server\n")) })
	switch {
	case *cert != "" && *key != "":
		log.Printf("velodesk server (wss) on %s", *addr)
		log.Fatal(http.ListenAndServeTLS(*addr, *cert, *key, mux))
	case *selfSigned:
		c, err := selfSignedCert()
		if err != nil {
			log.Fatal(err)
		}
		srv := &http.Server{Addr: *addr, Handler: mux, TLSConfig: &tls.Config{Certificates: []tls.Certificate{c}}}
		log.Printf("velodesk server (wss, self-signed) on %s", *addr)
		for _, ip := range localIPs() {
			log.Printf("  clients can use: %s%s", ip, *addr)
		}
		log.Fatal(srv.ListenAndServeTLS("", ""))
	default:
		log.Printf("velodesk server (ws, no TLS) on %s", *addr)
		for _, ip := range localIPs() {
			log.Printf("  clients can use: ws://%s%s", ip, *addr)
		}
		log.Fatal(http.ListenAndServe(*addr, mux))
	}
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			out = append(out, n.IP.String())
		}
	}
	return out
}

func selfSignedCert() (tls.Certificate, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "velodesk"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	for _, s := range localIPs() {
		tpl.IPAddresses = append(tpl.IPAddresses, net.ParseIP(s))
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, nil
}

// broadcast sends a message to every connected client without blocking.
func (h *hub) broadcast(m msg) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		select {
		case p.send <- m:
		default:
		}
	}
}

// watchUpdates tells every online client as soon as a new release is published.
func (h *hub) watchUpdates(manifest string) {
	var last time.Time
	size := int64(-1) // -1: no manifest yet, so its first appearance counts as a release
	if fi, err := os.Stat(manifest); err == nil {
		last, size = fi.ModTime(), fi.Size()
	}
	for ; ; time.Sleep(10 * time.Second) {
		fi, err := os.Stat(manifest)
		if err != nil {
			continue
		}
		if fi.ModTime() != last || fi.Size() != size {
			last, size = fi.ModTime(), fi.Size()
			log.Printf("new release published: notifying clients")
			h.broadcast(msg{T: "update"})
		}
	}
}
