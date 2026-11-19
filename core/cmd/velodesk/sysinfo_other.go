// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !windows

package main

import (
	"os"
	"strconv"
	"strings"
)

func totalMemMB() int {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "MemTotal:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				return kb / 1024
			}
		}
	}
	return 0
}
