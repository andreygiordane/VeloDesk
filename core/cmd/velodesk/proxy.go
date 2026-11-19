// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Proxy HTTP/HTTPS/SOCKS5 para o canal de sinalização e para as atualizações.
// O vídeo e o controle (WebRTC) não passam pelo proxy: eles vão direto ou pelo servidor TURN.

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// buildProxy devolve a função de escolha de proxy conforme as preferências:
//
//	proxyMode  never | auto (padrão) | custom
//	proxyType  http | https | socks5   proxyHost / proxyPort / proxyUser / proxyPass
//	noDirect   não cair para conexão direta quando o proxy não estiver disponível
func buildProxy(p map[string]any) func(*http.Request) (*url.URL, error) {
	noDirect := prefBool(p, "noDirect", false)
	var pick func(*http.Request) (*url.URL, error)
	switch prefStr(p, "proxyMode", "auto") {
	case "never":
	case "custom":
		if host := strings.TrimSpace(prefStr(p, "proxyHost", "")); host != "" {
			port := int(prefNum(p, "proxyPort", 8080))
			scheme := prefStr(p, "proxyType", "http")
			if scheme != "http" && scheme != "https" && scheme != "socks5" {
				scheme = "http"
			}
			u := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
			if usr := prefStr(p, "proxyUser", ""); usr != "" {
				u.User = url.UserPassword(usr, prefStr(p, "proxyPass", ""))
			}
			pick = http.ProxyURL(u)
		}
	default:
		pick = func(r *http.Request) (*url.URL, error) {
			if u := systemProxy(); u != nil {
				return u, nil
			}
			return http.ProxyFromEnvironment(r)
		}
	}
	return func(r *http.Request) (*url.URL, error) {
		var u *url.URL
		if pick != nil {
			var err error
			if u, err = pick(r); err != nil {
				return nil, err
			}
		}
		if u == nil && noDirect && !isLoopback(r.URL.Hostname()) {
			return nil, errors.New("conexão direta desabilitada e nenhum proxy disponível")
		}
		return u, nil
	}
}

func isLoopback(h string) bool {
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

func netTransport(p map[string]any, insecure bool) *http.Transport {
	t := &http.Transport{Proxy: buildProxy(p)}
	if d := authDialer(p); d != nil { // proxy com autenticação (NTLM/Basic): o túnel CONNECT é aberto por nós
		t.Proxy, t.DialContext = nil, d
	}
	if insecure {
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return t
}

func httpClientFor(p map[string]any, insecure bool) *http.Client {
	return &http.Client{Transport: netTransport(p, insecure)}
}
