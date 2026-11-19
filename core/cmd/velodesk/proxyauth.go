// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Autenticação do proxy: NTLM com as credenciais do Windows (SSPI), NTLM com usuário e senha, ou Basic.
// O handshake roda numa conexão CONNECT mantida aberta entre as respostas 407, porque o NTLM autentica a conexão.

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Azure/go-ntlmssp"
)

type ntlmClient interface {
	Negotiate() ([]byte, error)
	Authenticate(challenge []byte) ([]byte, error)
	Close()
}

type userNTLM struct{ user, pass string }

func (u userNTLM) Negotiate() ([]byte, error) {
	_, dom, _ := ntlmssp.GetDomain(u.user)
	return ntlmssp.NewNegotiateMessage(dom, "")
}
func (u userNTLM) Authenticate(ch []byte) ([]byte, error) {
	_, _, need := ntlmssp.GetDomain(u.user)
	return ntlmssp.ProcessChallenge(ch, u.user, u.pass, need)
}
func (userNTLM) Close() {}

// proxyAuthMode: none | windows | user. Preferências antigas com usuário preenchido contam como "user".
func proxyAuthMode(p map[string]any) string {
	switch m := prefStr(p, "proxyAuth", ""); m {
	case "none", "windows", "user":
		return m
	}
	if prefStr(p, "proxyUser", "") != "" {
		return "user"
	}
	return "none"
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

// offer devolve o token do esquema oferecido pelo proxy ("" se não oferecido) e se ele foi oferecido.
func offer(h http.Header, scheme string) (string, bool) {
	for _, v := range h.Values("Proxy-Authenticate") {
		v = strings.TrimSpace(v)
		if strings.EqualFold(v, scheme) {
			return "", true
		}
		if len(v) > len(scheme) && strings.EqualFold(v[:len(scheme)], scheme) && v[len(scheme)] == ' ' {
			return strings.TrimSpace(v[len(scheme)+1:]), true
		}
	}
	return "", false
}

// dialViaProxy abre um túnel CONNECT até target atravessando um proxy HTTP que exige autenticação.
func dialViaProxy(ctx context.Context, proxy *url.URL, target, auth, user, pass string) (net.Conn, error) {
	if proxy.Scheme != "http" && proxy.Scheme != "" {
		return nil, errors.New("a autenticação do proxy só funciona com proxy HTTP")
	}
	addr := proxy.Host
	if proxy.Port() == "" {
		addr = net.JoinHostPort(proxy.Hostname(), "8080")
	}
	c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(c)
	fail := func(e error) (net.Conn, error) { c.Close(); return nil, e }
	var nt ntlmClient
	defer func() {
		if nt != nil {
			nt.Close()
		}
	}()
	authz, sentFinal := "", false
	for round := 0; round < 4; round++ {
		req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\nProxy-Connection: Keep-Alive\r\n"
		if authz != "" {
			req += "Proxy-Authorization: " + authz + "\r\n"
		}
		c.SetDeadline(time.Now().Add(15 * time.Second))
		if _, err := io.WriteString(c, req+"\r\n"); err != nil {
			return fail(err)
		}
		resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
		if err != nil {
			return fail(err)
		}
		if resp.StatusCode == 200 {
			c.SetDeadline(time.Time{})
			if br.Buffered() > 0 {
				return &bufConn{c, br}, nil
			}
			return c, nil
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != 407 {
			return fail(fmt.Errorf("o proxy respondeu %s", resp.Status))
		}
		if sentFinal {
			return fail(errors.New("o proxy recusou as credenciais"))
		}
		switch {
		case nt != nil: // 2ª volta do NTLM: o proxy mandou o desafio
			tok, _ := offer(resp.Header, "NTLM")
			raw, derr := base64.StdEncoding.DecodeString(tok)
			if tok == "" || derr != nil {
				return fail(errors.New("o proxy não enviou o desafio NTLM"))
			}
			out, aerr := nt.Authenticate(raw)
			if aerr != nil {
				return fail(aerr)
			}
			authz, sentFinal = "NTLM "+base64.StdEncoding.EncodeToString(out), true
		default:
			_, hasNTLM := offer(resp.Header, "NTLM")
			_, hasBasic := offer(resp.Header, "Basic")
			switch {
			case hasNTLM && auth == "windows":
				if nt, err = newWinNTLM(); err != nil {
					return fail(err)
				}
			case hasNTLM && auth == "user":
				nt = userNTLM{user, pass}
			case hasBasic && auth == "user":
				authz, sentFinal = "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)), true
			case auth == "windows":
				return fail(errors.New("o proxy não oferece NTLM (a autenticação do Windows só funciona com NTLM)"))
			default:
				return fail(errors.New("o proxy exige uma autenticação que o VeloDesk não suporta"))
			}
			if nt != nil {
				msg, nerr := nt.Negotiate()
				if nerr != nil {
					return fail(nerr)
				}
				authz = "NTLM " + base64.StdEncoding.EncodeToString(msg)
			}
		}
	}
	return fail(errors.New("falha ao autenticar no proxy"))
}

// authDialer devolve o discador com autenticação de proxy, ou nil quando não há autenticação configurada.
func authDialer(p map[string]any) func(context.Context, string, string) (net.Conn, error) {
	auth := proxyAuthMode(p)
	if auth == "none" {
		return nil
	}
	pick := buildProxy(p)
	user, pass := prefStr(p, "proxyUser", ""), prefStr(p, "proxyPass", "")
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		u, err := pick(&http.Request{URL: &url.URL{Scheme: "https", Host: addr}})
		if err != nil {
			return nil, err
		}
		if u == nil {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
		}
		return dialViaProxy(ctx, u, addr, auth, user, pass)
	}
}
