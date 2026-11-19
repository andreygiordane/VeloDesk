// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Transferência de arquivos: os bytes chegam pelo canal WebRTC (criptografado) até a página local, e
// esta parte só grava no disco. Nada é executado nem aberto automaticamente.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var winReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)

// sanitizeName deixa só um nome de arquivo seguro (sem pastas, sem caracteres proibidos no Windows).
func sanitizeName(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = n[strings.LastIndex(n, "/")+1:]
	var b strings.Builder
	for _, r := range n {
		if r < 32 || strings.ContainsRune(`<>:"|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	n = strings.Trim(b.String(), " .")
	if rs := []rune(n); len(rs) > 120 {
		ext := filepath.Ext(n)
		if len([]rune(ext)) > 20 {
			ext = ""
		}
		n = string(rs[:120-len([]rune(ext))]) + ext
	}
	if n == "" {
		n = "arquivo"
	}
	if winReserved.MatchString(n) {
		n = "_" + n
	}
	return n
}

// createUnique cria o arquivo sem sobrescrever outro ("nome (1).ext").
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, n, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("não foi possível criar o arquivo")
}

type xfer struct {
	path          string
	size, written int64
	seen          time.Time
}

func registerFiles(mux *http.ServeMux, st *store, guard func(http.HandlerFunc) http.HandlerFunc,
	decode func(http.ResponseWriter, *http.Request, any) bool, jsonOut func(http.ResponseWriter, any), agentTok string) {
	var mu sync.Mutex
	active := map[string]*xfer{}

	dir := func() string {
		if d := prefStr(st.get().Prefs, "fileDir", ""); d != "" {
			return d
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Downloads", "VeloDesk")
	}
	drop := func(tid string) {
		mu.Lock()
		x := active[tid]
		delete(active, tid)
		mu.Unlock()
		if x != nil {
			os.Remove(x.path)
		}
	}

	mux.HandleFunc("/api/file/begin", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string
			Size int64
			Dir  string // pasta escolhida no gerenciador de arquivos (exige o token do agente)
		}
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		max := int64(prefNum(st.get().Prefs, "fileMaxMB", 2048)) << 20
		if in.Size < 0 || in.Size > max {
			http.Error(w, "Arquivo maior que o limite configurado", http.StatusRequestEntityTooLarge)
			return
		}
		d := dir()
		if in.Dir != "" {
			d = filepath.Clean(in.Dir)
			if !tokenOK(r, agentTok) || !filepath.IsAbs(d) || inDeny(d) {
				http.Error(w, "Pasta de destino não permitida", http.StatusForbidden)
				return
			}
			if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
				http.Error(w, "A pasta de destino não existe", http.StatusBadRequest)
				return
			}
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			http.Error(w, "Não foi possível usar a pasta de destino: "+err.Error(), http.StatusInternalServerError)
			return
		}
		f, name, err := createUnique(d, sanitizeName(in.Name))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f.Close()
		raw := make([]byte, 8)
		rand.Read(raw)
		tid := hex.EncodeToString(raw)
		mu.Lock()
		for k, x := range active { // descarta transferências abandonadas há mais de 1 h
			if time.Since(x.seen) > time.Hour {
				os.Remove(x.path)
				delete(active, k)
			}
		}
		active[tid] = &xfer{path: filepath.Join(d, name), size: in.Size, seen: time.Now()}
		mu.Unlock()
		jsonOut(w, map[string]string{"tid": tid, "name": name})
	}))

	mux.HandleFunc("/api/file/chunk", guard(func(w http.ResponseWriter, r *http.Request) {
		tid := r.URL.Query().Get("tid")
		mu.Lock()
		x := active[tid]
		mu.Unlock()
		if r.Method != http.MethodPost || x == nil {
			http.Error(w, "unknown transfer", http.StatusNotFound)
			return
		}
		body := http.MaxBytesReader(w, r.Body, 8<<20)
		f, err := os.OpenFile(x.path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, err := io.Copy(f, io.LimitReader(body, x.size-x.written+1))
		f.Close()
		mu.Lock()
		x.written += n
		x.seen = time.Now()
		over := x.written > x.size
		mu.Unlock()
		if err != nil || over {
			drop(tid)
			http.Error(w, "dados inválidos", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("/api/file/end", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TID string
			OK  bool
		}
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		mu.Lock()
		x := active[in.TID]
		mu.Unlock()
		if x == nil {
			jsonOut(w, map[string]bool{"ok": false})
			return
		}
		if !in.OK || x.written != x.size {
			drop(in.TID)
			jsonOut(w, map[string]bool{"ok": false})
			return
		}
		mu.Lock()
		delete(active, in.TID)
		mu.Unlock()
		jsonOut(w, map[string]any{"ok": true, "name": filepath.Base(x.path), "size": x.size})
	}))

	mux.HandleFunc("/api/file/dir", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct{ Dir string }
			if !decode(w, r, &in) {
				return
			}
			d := strings.TrimSpace(in.Dir)
			if d != "" {
				if !filepath.IsAbs(d) {
					http.Error(w, "Use um caminho completo (ex.: C:\\Recebidos)", http.StatusBadRequest)
					return
				}
				if err := os.MkdirAll(d, 0o700); err != nil {
					http.Error(w, "Não foi possível usar essa pasta: "+err.Error(), http.StatusBadRequest)
					return
				}
				d = filepath.Clean(d)
			}
			jsonOut(w, map[string]string{"dir": d})
			return
		}
		jsonOut(w, map[string]string{"dir": dir()})
	}))

	// Mostra o arquivo recebido na pasta (só o nome, nunca um caminho vindo da página).
	mux.HandleFunc("/api/file/reveal", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		p := filepath.Join(dir(), filepath.Base(in.Name))
		if _, err := os.Stat(p); err != nil {
			p = dir()
		}
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				cmd = exec.Command("explorer", "/select,"+p)
			} else {
				cmd = exec.Command("explorer", p)
			}
		case "darwin":
			cmd = exec.Command("open", "-R", p)
		default:
			cmd = exec.Command("xdg-open", dir())
		}
		cmd.Start()
		jsonOut(w, map[string]bool{"ok": true})
	}))
}
