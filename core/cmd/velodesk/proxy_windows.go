// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build windows

package main

import (
	"net/url"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxy lê o proxy configurado no Windows (Configurações > Rede > Proxy).
func systemProxy() *url.URL {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if on, _, err := k.GetIntegerValue("ProxyEnable"); err != nil || on == 0 {
		return nil
	}
	srv, _, err := k.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(srv) == "" {
		return nil
	}
	pick := srv
	if strings.Contains(srv, "=") { // "http=h:80;https=h:443"
		pick = ""
		for _, part := range strings.Split(srv, ";") {
			if kv := strings.SplitN(part, "=", 2); len(kv) == 2 && (kv[0] == "https" || (kv[0] == "http" && pick == "")) {
				pick = kv[1]
			}
		}
	}
	if pick == "" {
		return nil
	}
	if !strings.Contains(pick, "://") {
		pick = "http://" + pick
	}
	u, err := url.Parse(pick)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}
