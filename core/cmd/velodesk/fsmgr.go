// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Gerenciador de arquivos remoto (lado do computador controlado) e impressão remota. Todas as rotas exigem o token do
// agente: a página só as chama depois de conferir a permissão do perfil da sessão.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

func isRoot(p string) bool {
	rest := p[len(filepath.VolumeName(p)):]
	return rest == "\\" || rest == "/" || rest == ""
}

func denyDirs() []string {
	if runtime.GOOS == "windows" {
		return []string{os.Getenv("SystemRoot"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")}
	}
	return []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/sbin", "/sys", "/usr", "/var/lib"}
}

// inDeny: dentro de pastas do sistema, onde nada é criado, renomeado nem apagado pelo gerenciador.
func inDeny(p string) bool {
	low := strings.ToLower(filepath.Clean(p))
	for _, d := range denyDirs() {
		if d == "" {
			continue
		}
		d = strings.ToLower(filepath.Clean(d))
		if low == d || strings.HasPrefix(low, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// protectedPath: raízes de disco, pastas do sistema e a pasta pessoal não podem ser renomeadas nem apagadas.
func protectedPath(p string) bool {
	p = filepath.Clean(p)
	if isRoot(p) || inDeny(p) {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && strings.EqualFold(filepath.Clean(home), p) {
		return true
	}
	return false
}

type fsEntry struct {
	N string `json:"n"`
	P string `json:"p"`
	D bool   `json:"d"`
	S int64  `json:"s"`
	M int64  `json:"m"`
}

func rootsList() []fsEntry {
	var out []fsEntry
	if runtime.GOOS == "windows" {
		for c := 'A'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if _, err := os.Stat(d); err == nil {
				out = append(out, fsEntry{N: d, P: d, D: true})
			}
		}
	} else {
		out = append(out, fsEntry{N: "/", P: "/", D: true})
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append([]fsEntry{{N: "Pasta pessoal", P: home, D: true}}, out...)
	}
	return out
}

func registerFS(mux *http.ServeMux, st *store, guard func(http.HandlerFunc) http.HandlerFunc,
	decode func(http.ResponseWriter, *http.Request, any) bool, jsonOut func(http.ResponseWriter, any), agentTok string) {
	allowed := func(w http.ResponseWriter, r *http.Request) bool {
		if !tokenOK(r, agentTok) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return false
		}
		return true
	}
	absPath := func(w http.ResponseWriter, p string) (string, bool) {
		p = filepath.Clean(p)
		if !filepath.IsAbs(p) {
			http.Error(w, "caminho inválido", http.StatusBadRequest)
			return "", false
		}
		return p, true
	}

	mux.HandleFunc("/api/fs/list", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		if !allowed(w, r) || r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Path) == "" {
			jsonOut(w, map[string]any{"path": "", "parent": "", "entries": rootsList()})
			return
		}
		p, ok := absPath(w, in.Path)
		if !ok {
			return
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			http.Error(w, "Não foi possível abrir a pasta: "+err.Error(), http.StatusBadRequest)
			return
		}
		list := make([]fsEntry, 0, len(ents))
		for _, e := range ents {
			if len(list) >= 3000 {
				break
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			list = append(list, fsEntry{N: e.Name(), P: filepath.Join(p, e.Name()), D: e.IsDir(), S: fi.Size(), M: fi.ModTime().Unix()})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].D != list[j].D {
				return list[i].D
			}
			return strings.ToLower(list[i].N) < strings.ToLower(list[j].N)
		})
		parent := filepath.Dir(p)
		if isRoot(p) {
			parent = ""
		}
		jsonOut(w, map[string]any{"path": p, "parent": parent, "entries": list})
	}))

	mux.HandleFunc("/api/fs/mkdir", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path, Name string }
		if !allowed(w, r) || r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		p, ok := absPath(w, in.Path)
		if !ok {
			return
		}
		t := filepath.Join(p, sanitizeName(in.Name))
		if inDeny(t) {
			http.Error(w, "Esta pasta é protegida", http.StatusForbidden)
			return
		}
		if err := os.Mkdir(t, 0o755); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/fs/rename", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path, Name string }
		if !allowed(w, r) || r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		p, ok := absPath(w, in.Path)
		if !ok {
			return
		}
		t := filepath.Join(filepath.Dir(p), sanitizeName(in.Name))
		if protectedPath(p) || inDeny(t) {
			http.Error(w, "Este item é protegido", http.StatusForbidden)
			return
		}
		if _, err := os.Stat(t); err == nil {
			http.Error(w, "Já existe um item com esse nome", http.StatusConflict)
			return
		}
		if err := os.Rename(p, t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	}))

	mux.HandleFunc("/api/fs/delete", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		if !allowed(w, r) || r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		p, ok := absPath(w, in.Path)
		if !ok {
			return
		}
		if protectedPath(p) {
			http.Error(w, "Este item é protegido", http.StatusForbidden)
			return
		}
		if _, err := os.Stat(p); err != nil {
			http.Error(w, "Item não encontrado", http.StatusNotFound)
			return
		}
		if err := os.RemoveAll(p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	}))

	// leitura em blocos de um arquivo (para o controlador baixar)
	mux.HandleFunc("/api/fs/read", guard(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		p, ok := absPath(w, r.URL.Query().Get("path"))
		if !ok {
			return
		}
		f, err := os.Open(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			http.Error(w, "não é um arquivo", http.StatusBadRequest)
			return
		}
		off, n := int64(0), int64(1<<20)
		if v := r.URL.Query().Get("off"); v != "" {
			off = atoi64(v)
		}
		if v := r.URL.Query().Get("n"); v != "" && atoi64(v) < n {
			n = atoi64(v)
		}
		buf := make([]byte, n)
		got, _ := f.ReadAt(buf, off)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-File-Size", strconv.FormatInt(fi.Size(), 10))
		w.Write(buf[:got])
	}))

	// ---- impressão remota ----
	mux.HandleFunc("/api/printers", guard(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, r) {
			return
		}
		jsonOut(w, map[string]any{"printers": listPrinters()})
	}))
	mux.HandleFunc("/api/print", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Printer string }
		if !allowed(w, r) || r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		dir := prefStr(st.get().Prefs, "fileDir", "")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, "Downloads", "VeloDesk")
		}
		path := filepath.Join(dir, filepath.Base(in.Name))
		if !printableExt(path) {
			http.Error(w, "Só é possível imprimir PDF, TXT e imagens (PNG, JPG, BMP, GIF)", http.StatusBadRequest)
			return
		}
		if _, err := os.Stat(path); err != nil {
			http.Error(w, "arquivo não encontrado", http.StatusNotFound)
			return
		}
		if err := printFile(path, in.Printer); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOut(w, map[string]bool{"ok": true})
	}))
}

func atoi64(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

var printExts = map[string]bool{".pdf": true, ".txt": true, ".png": true, ".jpg": true, ".jpeg": true, ".bmp": true, ".gif": true}

func printableExt(p string) bool { return printExts[strings.ToLower(filepath.Ext(p))] }

func listPrinters() []string {
	if os.Getenv("VELODESK_PRINT_DRYRUN") != "" {
		return []string{"Impressora de teste"}
	}
	var out []string
	switch runtime.GOOS {
	case "windows":
		b, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-CimInstance Win32_Printer | Select-Object -ExpandProperty Name | ConvertTo-Json -Compress").Output()
		if err == nil {
			var many []string
			if json.Unmarshal(b, &many) != nil {
				var one string
				if json.Unmarshal(b, &one) == nil && one != "" {
					many = []string{one}
				}
			}
			out = many
		}
	default:
		if b, err := exec.Command("lpstat", "-e").Output(); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					out = append(out, l)
				}
			}
		}
	}
	return out
}

// printFile imprime na impressora escolhida ("" = padrão). Com VELODESK_PRINT_DRYRUN=<arquivo> só registra o pedido (testes).
func printFile(path, printer string) error {
	if dry := os.Getenv("VELODESK_PRINT_DRYRUN"); dry != "" {
		f, err := os.OpenFile(dry, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(printer + "|" + path + "\n")
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `if ($env:VD_PRINTER) { Start-Process -FilePath $env:VD_FILE -Verb PrintTo -ArgumentList ('"' + $env:VD_PRINTER + '"') -WindowStyle Hidden } else { Start-Process -FilePath $env:VD_FILE -Verb Print -WindowStyle Hidden }`
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Env = append(os.Environ(), "VD_FILE="+path, "VD_PRINTER="+printer)
	default:
		args := []string{}
		if printer != "" {
			args = append(args, "-d", printer)
		}
		cmd = exec.Command("lp", append(args, "--", path)...)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { time.Sleep(time.Minute); cmd.Process.Kill() }()
	go cmd.Wait()
	return nil
}
