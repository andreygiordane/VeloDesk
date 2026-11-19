// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

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
	"time"
)

func echoServer(t *testing.T) net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return l
}

func roundTrip(t *testing.T, c net.Conn, msg string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(c, msg); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("eco pelo túnel: %q %v", buf, err)
	}
}

func bridge(a, b net.Conn) {
	go func() { io.Copy(a, b); a.Close() }()
	io.Copy(b, a)
	b.Close()
}

// proxy falso que exige Basic ana:segredo
func basicProxy(t *testing.T) net.Listener {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("ana:segredo"))
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				br := bufio.NewReader(c)
				for {
					req, err := http.ReadRequest(br)
					if err != nil {
						c.Close()
						return
					}
					if req.Header.Get("Proxy-Authorization") != want {
						io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"x\"\r\nContent-Length: 0\r\n\r\n")
						continue
					}
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
	return l
}

func TestBasicProxyAuth(t *testing.T) {
	echo, px := echoServer(t), basicProxy(t)
	defer echo.Close()
	defer px.Close()
	port := float64(px.Addr().(*net.TCPAddr).Port)
	good := map[string]any{"proxyMode": "custom", "proxyHost": "127.0.0.1", "proxyPort": port, "proxyAuth": "user", "proxyUser": "ana", "proxyPass": "segredo"}
	c, err := authDialer(good)(context.Background(), "tcp", echo.Addr().String())
	if err != nil {
		t.Fatalf("login correto falhou: %v", err)
	}
	roundTrip(t, c, "ola pelo proxy")
	c.Close()
	bad := map[string]any{"proxyMode": "custom", "proxyHost": "127.0.0.1", "proxyPort": port, "proxyAuth": "user", "proxyUser": "ana", "proxyPass": "errada"}
	if c, err := authDialer(bad)(context.Background(), "tcp", echo.Addr().String()); err == nil {
		c.Close()
		t.Fatal("senha errada foi aceita pelo proxy")
	} else if !strings.Contains(err.Error(), "recusou") {
		t.Fatalf("erro inesperado: %v", err)
	}
	// sem autenticação configurada o discador nem entra em ação
	if authDialer(map[string]any{"proxyMode": "custom", "proxyHost": "127.0.0.1", "proxyAuth": "none"}) != nil {
		t.Fatal("autenticação 'none' não deveria criar discador")
	}
	// modo "windows" com proxy que só oferece Basic: erro claro
	win := map[string]any{"proxyMode": "custom", "proxyHost": "127.0.0.1", "proxyPort": port, "proxyAuth": "windows"}
	if c, err := authDialer(win)(context.Background(), "tcp", echo.Addr().String()); err == nil {
		c.Close()
		t.Fatal("windows auth sem NTLM no proxy deveria falhar")
	}
}

func TestUserNTLMNegotiateMessage(t *testing.T) {
	msg, err := userNTLM{`CORP\ana`, "x"}.Negotiate()
	if err != nil || len(msg) < 16 || string(msg[:8]) != "NTLMSSP\x00" {
		t.Fatalf("mensagem NTLM tipo 1 inválida: %q %v", msg, err)
	}
}

func TestProxyAuthModeLegacy(t *testing.T) {
	if proxyAuthMode(map[string]any{"proxyUser": "x"}) != "user" || proxyAuthMode(map[string]any{}) != "none" || proxyAuthMode(map[string]any{"proxyAuth": "windows"}) != "windows" {
		t.Fatal("modo de autenticação")
	}
}
