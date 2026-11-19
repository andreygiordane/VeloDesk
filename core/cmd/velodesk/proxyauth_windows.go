// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build windows

package main

import (
	"github.com/alexbrainman/sspi"
	"github.com/alexbrainman/sspi/ntlm"
)

// winNTLM usa a conta do Windows que está logada (SSPI), sem pedir senha.
type winNTLM struct {
	cred *sspi.Credentials
	ctx  *ntlm.ClientContext
}

func newWinNTLM() (ntlmClient, error) {
	cred, err := ntlm.AcquireCurrentUserCredentials()
	if err != nil {
		return nil, err
	}
	return &winNTLM{cred: cred}, nil
}

func (w *winNTLM) Negotiate() ([]byte, error) {
	ctx, msg, err := ntlm.NewClientContext(w.cred)
	w.ctx = ctx
	return msg, err
}

func (w *winNTLM) Authenticate(ch []byte) ([]byte, error) { return w.ctx.Update(ch) }

func (w *winNTLM) Close() {
	if w.ctx != nil {
		w.ctx.Release()
	}
	w.cred.Release()
}
