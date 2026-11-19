// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !linux && !windows

package agent

import "errors"

func newBackend() (backend, error) {
	return nil, errors.New("remote control is not supported on this operating system yet")
}
