// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"relatorio.pdf":                    "relatorio.pdf",
		`..\..\Windows\System32\evil.dll`:  "evil.dll",
		"../../etc/passwd":                 "passwd",
		`a<b>c:d"e|f?g*h.txt`:              "a_b_c_d_e_f_g_h.txt",
		"  ..oculto.txt.. ":                "oculto.txt",
		"CON":                              "_CON",
		"nul.txt":                          "_nul.txt",
		"":                                 "arquivo",
		"...":                              "arquivo",
		"C:\\Users\\x\\foto.png":           "foto.png",
		"com\x00mando\x1f.bat":             "com_mando_.bat",
		strings.Repeat("a", 300) + ".docx": strings.Repeat("a", 115) + ".docx",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func fileServer(t *testing.T, prefs map[string]any) (*httptest.Server, string) {
	dir := t.TempDir()
	prefs["fileDir"] = dir
	st := &store{path: filepath.Join(dir, "cfg.json"), c: config{Prefs: prefs}}
	mux := http.NewServeMux()
	id := func(h http.HandlerFunc) http.HandlerFunc { return h }
	registerFiles(mux, st, id, func(w http.ResponseWriter, r *http.Request, v any) bool {
		return json.NewDecoder(r.Body).Decode(v) == nil
	}, func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }, "tok")
	return httptest.NewServer(mux), dir
}

func post(t *testing.T, url string, body any) (*http.Response, map[string]any) {
	b, _ := json.Marshal(body)
	r, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	return r, out
}

func TestFileTransferFlow(t *testing.T) {
	srv, dir := fileServer(t, map[string]any{"fileMaxMB": 1.0})
	defer srv.Close()

	// nome malicioso vira um nome seguro dentro da pasta de destino
	r, out := post(t, srv.URL+"/api/file/begin", map[string]any{"name": `..\..\fora.txt`, "size": 11})
	if r.StatusCode != 200 || out["name"] != "fora.txt" {
		t.Fatalf("begin: %d %v", r.StatusCode, out)
	}
	tid := out["tid"].(string)
	for _, part := range []string{"ola ", "mundo", "!!"} { // 11 bytes em 3 pedaÃ§os (o Ãºltimo passa de 11? "ola mundo!!" = 11)
		resp, _ := http.Post(srv.URL+"/api/file/chunk?tid="+tid, "application/octet-stream", strings.NewReader(part))
		if resp.StatusCode != 204 {
			t.Fatalf("chunk %q: %d", part, resp.StatusCode)
		}
	}
	_, end := post(t, srv.URL+"/api/file/end", map[string]any{"tid": tid, "ok": true})
	if end["ok"] != true {
		t.Fatalf("end: %v", end)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fora.txt")); string(b) != "ola mundo!!" {
		t.Fatalf("conteÃºdo gravado: %q", b)
	}

	// o mesmo nome nÃ£o sobrescreve
	_, out2 := post(t, srv.URL+"/api/file/begin", map[string]any{"name": "fora.txt", "size": 1})
	if out2["name"] != "fora (1).txt" {
		t.Fatalf("segundo arquivo: %v", out2)
	}

	// maior que o limite Ã© recusado jÃ¡ no inÃ­cio
	if r, _ := post(t, srv.URL+"/api/file/begin", map[string]any{"name": "grande.bin", "size": 5 << 20}); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("limite de tamanho nÃ£o aplicado: %d", r.StatusCode)
	}

	// mais bytes do que o declarado derruba e apaga o arquivo
	_, o3 := post(t, srv.URL+"/api/file/begin", map[string]any{"name": "curto.txt", "size": 3})
	resp, _ := http.Post(srv.URL+"/api/file/chunk?tid="+o3["tid"].(string), "application/octet-stream", strings.NewReader("12345"))
	if resp.StatusCode == 204 {
		t.Fatal("excesso de dados aceito")
	}
	if _, err := os.Stat(filepath.Join(dir, "curto.txt")); err == nil {
		t.Fatal("arquivo parcial nÃ£o foi apagado")
	}

	// cancelar (ok=false) apaga o parcial; tamanho incompleto tambÃ©m
	_, o4 := post(t, srv.URL+"/api/file/begin", map[string]any{"name": "meio.txt", "size": 10})
	http.Post(srv.URL+"/api/file/chunk?tid="+o4["tid"].(string), "application/octet-stream", strings.NewReader("123"))
	_, e4 := post(t, srv.URL+"/api/file/end", map[string]any{"tid": o4["tid"], "ok": true})
	if e4["ok"] != false {
		t.Fatalf("transferÃªncia incompleta foi aceita: %v", e4)
	}
	if _, err := os.Stat(filepath.Join(dir, "meio.txt")); err == nil {
		t.Fatal("arquivo incompleto nÃ£o foi apagado")
	}
}
