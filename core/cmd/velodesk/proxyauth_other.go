// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !windows

package main

import "errors"

func newWinNTLM() (ntlmClient, error) {
	return nil, errors.New("a autenticação do Windows só existe no Windows (use usuário e senha)")
}
