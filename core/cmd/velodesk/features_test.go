// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMagicPacket(t *testing.T) {
	mac, err := parseMAC("aa-bb-cc-dd-ee-01")
	if err != nil {
		t.Fatal(err)
	}
	p := magicPacket(mac)
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) || !bytes.Equal(p[96:], mac) {
		t.Fatalf("pacote mágico inválido (%d bytes)", len(p))
	}
	for _, bad := range []string{"", "xx", "aa:bb:cc", "aa:bb:cc:dd:ee:ff:00:11"} {
		if _, err := parseMAC(bad); err == nil {
			t.Errorf("MAC %q deveria ser recusado", bad)
		}
	}
}

func TestTunnelDestinationPolicy(t *testing.T) {
	for _, ok := range []string{"127.0.0.1", "localhost", "192.168.1.20", "10.0.0.5", "172.16.4.2", "::1", "169.254.1.1"} {
		if !destAllowed(ok) {
			t.Errorf("%s deveria ser permitido", ok)
		}
	}
	for _, no := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "", "dominio-que-nao-existe.invalid"} {
		if destAllowed(no) {
			t.Errorf("%s deveria ser bloqueado", no)
		}
	}
}

func TestWallpaperThumbnail(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			img.Set(x, y, color.RGBA{uint8(x / 8), uint8(y / 5), 120, 255})
		}
	}
	var src bytes.Buffer
	png.Encode(&src, img)
	out, err := thumbJPEG(&src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil || got.Bounds().Dx() != 320 || got.Bounds().Dy() != 200 || len(out) > 60<<10 {
		t.Fatalf("miniatura inválida: %v %v %dB", err, got.Bounds(), len(out))
	}
	if _, err := thumbJPEG(strings.NewReader("isto não é imagem")); err == nil {
		t.Fatal("lixo foi aceito como imagem")
	}
}

func fsServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	st := &store{path: filepath.Join(t.TempDir(), "c.json"), c: config{Prefs: map[string]any{}}}
	id := func(h http.HandlerFunc) http.HandlerFunc { return h }
	registerFS(mux, st, id, func(w http.ResponseWriter, r *http.Request, v any) bool {
		return json.NewDecoder(r.Body).Decode(v) == nil
	},
		func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }, "tok")
	return httptest.NewServer(mux)
}

func fsPost(t *testing.T, base, path string, body any, token string) (int, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("X-Agent-Token", token)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	return r.StatusCode, out
}

func TestFileManagerOps(t *testing.T) {
	srv := fsServer(t)
	defer srv.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("conteudo"), 0o644)
	os.Mkdir(filepath.Join(dir, "A pasta"), 0o755)

	if code, _ := fsPost(t, srv.URL, "/api/fs/list", map[string]any{"path": dir}, ""); code != 403 {
		t.Fatalf("sem token deveria ser 403, veio %d", code)
	}
	code, out := fsPost(t, srv.URL, "/api/fs/list", map[string]any{"path": dir}, "tok")
	ents := out["entries"].([]any)
	if code != 200 || len(ents) != 2 || ents[0].(map[string]any)["d"] != true {
		t.Fatalf("listagem (pastas primeiro): %d %v", code, out)
	}
	if code, _ := fsPost(t, srv.URL, "/api/fs/list", map[string]any{"path": "relativo/x"}, "tok"); code != 400 {
		t.Fatalf("caminho relativo deveria ser recusado: %d", code)
	}
	if c, _ := fsPost(t, srv.URL, "/api/fs/mkdir", map[string]any{"path": dir, "name": `nova/../x`}, "tok"); c != 200 {
		t.Fatal("mkdir")
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err != nil { // nome sanitizado, sem escapar da pasta
		t.Fatalf("pasta criada com nome seguro: %v", err)
	}
	if c, _ := fsPost(t, srv.URL, "/api/fs/rename", map[string]any{"path": filepath.Join(dir, "b.txt"), "name": "c.txt"}, "tok"); c != 200 {
		t.Fatal("rename")
	}
	if c, _ := fsPost(t, srv.URL, "/api/fs/rename", map[string]any{"path": filepath.Join(dir, "c.txt"), "name": "x"}, "tok"); c != 409 {
		t.Fatalf("renomear para nome existente deveria dar 409, deu %d", c)
	}
	// leitura em blocos
	req, _ := http.NewRequest("GET", srv.URL+"/api/fs/read?path="+filepath.Join(dir, "c.txt")+"&off=3&n=4", nil)
	req.Header.Set("X-Agent-Token", "tok")
	r, _ := http.DefaultClient.Do(req)
	buf := new(bytes.Buffer)
	buf.ReadFrom(r.Body)
	if buf.String() != "teud" {
		t.Fatalf("leitura em bloco: %q", buf.String())
	}
	// itens protegidos
	home, _ := os.UserHomeDir()
	for _, p := range []string{home, filepath.VolumeName(dir) + string(filepath.Separator), os.Getenv("SystemRoot")} {
		if p == "" {
			continue
		}
		if c, _ := fsPost(t, srv.URL, "/api/fs/delete", map[string]any{"path": p}, "tok"); c != 403 {
			t.Errorf("apagar %q deveria ser bloqueado (veio %d)", p, c)
		}
	}
	if c, _ := fsPost(t, srv.URL, "/api/fs/delete", map[string]any{"path": filepath.Join(dir, "c.txt")}, "tok"); c != 200 {
		t.Fatal("delete")
	}
	if _, err := os.Stat(filepath.Join(dir, "c.txt")); err == nil {
		t.Fatal("arquivo não foi apagado")
	}
	_, roots := fsPost(t, srv.URL, "/api/fs/list", map[string]any{"path": ""}, "tok")
	if len(roots["entries"].([]any)) == 0 {
		t.Fatal("raízes vazias")
	}
}

func TestPrintDryRunAndExtensions(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "print.log")
	t.Setenv("VELODESK_PRINT_DRYRUN", log)
	mux := http.NewServeMux()
	st := &store{path: filepath.Join(dir, "c.json"), c: config{Prefs: map[string]any{"fileDir": dir}}}
	id := func(h http.HandlerFunc) http.HandlerFunc { return h }
	registerFS(mux, st, id, func(w http.ResponseWriter, r *http.Request, v any) bool {
		return json.NewDecoder(r.Body).Decode(v) == nil
	},
		func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }, "tok")
	srv := httptest.NewServer(mux)
	defer srv.Close()
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4"), 0o644)
	os.WriteFile(filepath.Join(dir, "virus.exe"), []byte("MZ"), 0o644)
	if c, _ := fsPost(t, srv.URL, "/api/print", map[string]any{"name": "doc.pdf", "printer": "HP"}, "tok"); c != 200 {
		t.Fatalf("imprimir pdf: %d", c)
	}
	if c, _ := fsPost(t, srv.URL, "/api/print", map[string]any{"name": "virus.exe", "printer": "HP"}, "tok"); c != 400 {
		t.Fatalf("executável não pode ser impresso: %d", c)
	}
	if c, _ := fsPost(t, srv.URL, "/api/print", map[string]any{"name": `..\..\doc.pdf`, "printer": "HP"}, "tok"); c != 200 { // só o nome base conta
		t.Fatalf("nome base: %d", c)
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "HP|") != 2 || strings.Contains(string(b), "virus") {
		t.Fatalf("registro de impressão: %q", b)
	}
	_ = net.IPv4zero
}
