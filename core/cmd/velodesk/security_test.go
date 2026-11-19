// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Vetor oficial da RFC 6238 (SHA-1, segredo "12345678901234567890", t=59s => 94287082; 6 dígitos: 287082).
func TestTOTPVector(t *testing.T) {
	if got := totpAt([]byte("12345678901234567890"), 59/30); got != "287082" {
		t.Fatalf("totp = %s, want 287082", got)
	}
}

func TestTOTPReplayAndWindow(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	now := time.Now().Unix() / 30
	code := totpAt([]byte("12345678901234567890"), now)
	var last int64
	if !checkTOTP(secret, code, &last) {
		t.Fatal("código válido recusado")
	}
	if checkTOTP(secret, code, &last) {
		t.Fatal("o mesmo código foi aceito duas vezes (replay)")
	}
	if checkTOTP(secret, "000000", new(int64)) && totpAt([]byte("12345678901234567890"), now) != "000000" {
		t.Fatal("código errado aceito")
	}
}

// Os mesmos valores foram gerados no Node (crypto.pbkdf2Sync / createHmac), igual ao WebCrypto do navegador.
func TestPBKDF2AndHMACMatchBrowser(t *testing.T) {
	salt, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	key := hashPassword("minhaSenha#2026", salt)
	if key != "59176ca001c1a5ebb53ead52ea9ff10d392179dda8a4463c0998ef7903792988" {
		t.Fatalf("pbkdf2 diferente do navegador: %s", key)
	}
	raw, _ := hex.DecodeString(key)
	mac := macHex(raw, "velodesk2|111111111|222222222|vn|hn")
	if mac != "d0ea641901c3ac598b7836fbb3bf7cd5aa344b09a650ec2b4747a8881b7f4cc5" {
		t.Fatalf("hmac diferente do navegador: %s", mac)
	}
	if !macEq(raw, "velodesk2|111111111|222222222|vn|hn", mac) || macEq(raw, "outro", mac) {
		t.Fatal("macEq incorreto")
	}
}

func TestPasswordProblem(t *testing.T) {
	for _, bad := range []string{"1234567", "12345678", "aaaaaaaaa", "Password"} {
		if passwordProblem(bad) == "" {
			t.Errorf("%q deveria ser recusada", bad)
		}
	}
	if passwordProblem("vento-azul-42") != "" {
		t.Error("senha boa recusada")
	}
}

func TestProxyModes(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://remoto.exemplo.com/ws", nil)

	if u, err := buildProxy(map[string]any{"proxyMode": "never"})(req); u != nil || err != nil {
		t.Fatalf("never: %v %v", u, err)
	}
	u, err := buildProxy(map[string]any{"proxyMode": "custom", "proxyType": "http", "proxyHost": "10.0.0.5", "proxyPort": 3128.0, "proxyUser": "ana", "proxyPass": "x"})(req)
	if err != nil || u == nil || u.Host != "10.0.0.5:3128" || u.User.Username() != "ana" {
		t.Fatalf("custom: %v %v", u, err)
	}
	// sem proxy e sem conexão direta => erro; servidor local continua permitido
	if _, err := buildProxy(map[string]any{"proxyMode": "never", "noDirect": true})(req); err == nil {
		t.Fatal("noDirect deveria bloquear a conexão direta")
	}
	local, _ := http.NewRequest("GET", "ws://127.0.0.1:21116/ws", &nilBody{})
	local.URL, _ = url.Parse("http://127.0.0.1:21116/ws")
	if _, err := buildProxy(map[string]any{"proxyMode": "never", "noDirect": true})(local); err != nil {
		t.Fatalf("loopback deveria ser sempre permitido: %v", err)
	}
}

type nilBody struct{}

func (*nilBody) Read([]byte) (int, error) { return 0, nil }
