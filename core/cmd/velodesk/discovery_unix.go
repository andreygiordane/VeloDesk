// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !windows

package main

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// reuseControl deixa mais de um VeloDesk ouvir a mesma porta UDP (necessário para o broadcast).
func reuseControl(network, address string, c syscall.RawConn) error {
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
		if serr == nil {
			_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
		}
	}); err != nil {
		return err
	}
	return serr
}
