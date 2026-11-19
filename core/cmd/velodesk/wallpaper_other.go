// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !windows

package main

import (
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// wallpaperPath: GNOME (gsettings) e macOS (System Events). Outros ambientes ficam sem miniatura de papel de parede.
func wallpaperPath() string {
	switch runtime.GOOS {
	case "darwin":
		if b, err := exec.Command("osascript", "-e", `tell application "System Events" to get picture of desktop 1`).Output(); err == nil {
			return strings.TrimSpace(string(b))
		}
	case "linux":
		for _, key := range []string{"picture-uri-dark", "picture-uri"} {
			b, err := exec.Command("gsettings", "get", "org.gnome.desktop.background", key).Output()
			if err != nil {
				continue
			}
			v := strings.Trim(strings.TrimSpace(string(b)), "'\"")
			if u, err := url.Parse(v); err == nil && u.Scheme == "file" {
				if _, err := os.Stat(u.Path); err == nil {
					return u.Path
				}
			}
		}
	}
	return ""
}
