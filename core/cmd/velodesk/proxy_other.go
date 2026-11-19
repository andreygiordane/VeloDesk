// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build !windows

package main

import "net/url"

// systemProxy: fora do Windows só valem as variáveis HTTP_PROXY/HTTPS_PROXY (tratadas por net/http).
func systemProxy() *url.URL { return nil }
