// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build windows

package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/alexbrainman/sspi/ntlm"
)

// Proxy falso que valida o NTLM com o SSPI do próprio Windows: prova que o handshake real funciona com a conta logada.
func TestNTLMWindowsProxy(t *testing.T) {
	cred, err := ntlm.AcquireServerCredentials()
	if err != nil {
		t.Skipf("SSPI indisponível: %v", err)
	}
	defer cred.Release()
	echo := echoServer(t)
	defer echo.Close()
	px, _ := net.Listen("tcp", "127.0.0.1:0")
	defer px.Close()
	authed := make(chan bool, 4)
	go func() {
		for {
			c, err := px.Accept()
			if err != nil {
				return
			}
			go func() {
				br := bufio.NewReader(c)
				var sc *ntlm.ServerContext
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						c.Close()
						return
					}
					a := req.Header.Get("Proxy-Authorization")
					if !strings.HasPrefix(a, "NTLM ") {
						io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: NTLM\r\nContent-Length: 0\r\n\r\n")
						continue
					}
					raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "NTLM "))
					if sc == nil {
						var ch []byte
						if sc, ch, err = ntlm.NewServerContext(cred, raw); err != nil {
							c.Close()
							return
						}
						io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: NTLM "+base64.StdEncoding.EncodeToString(ch)+"\r\nContent-Length: 0\r\n\r\n")
						continue
					}
					if err := sc.Update(raw); err != nil { // o Windows confere a prova do cliente
						authed <- false
						io.WriteString(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
						c.Close()
						return
					}
					authed <- true
					dst, err := net.Dial("tcp", req.Host)
					if err != nil {
						c.Close()
						return
					}
					io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n")
					bridge(c, dst)
					return
				}
			}()
		}
	}()
	p := map[string]any{"proxyMode": "custom", "proxyHost": "127.0.0.1", "proxyPort": float64(px.Addr().(*net.TCPAddr).Port), "proxyAuth": "windows"}
	c, err := authDialer(p)(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("NTLM do Windows pelo proxy: %v", err)
	}
	defer c.Close()
	roundTrip(t, c, "tráfego autenticado por NTLM")
	if ok := <-authed; !ok {
		t.Fatal("o proxy rejeitou a prova NTLM")
	}
}
