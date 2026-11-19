// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build windows

package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// wallpaperPath: o arquivo "transcodificado" do Windows é sempre o papel de parede atual (inclusive em apresentação de slides).
func wallpaperPath() string {
	if app := os.Getenv("APPDATA"); app != "" {
		p := filepath.Join(app, `Microsoft\Windows\Themes\TranscodedWallpaper`)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\Desktop`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		if p, _, err := k.GetStringValue("WallPaper"); err == nil && p != "" {
			return p
		}
	}
	return ""
}
