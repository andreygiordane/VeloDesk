// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// Segurança do host: verificação de senha por desafio (a senha nunca trafega), 2FA (TOTP),
// tokens de "acesso salvo" e de curto prazo, ações remotas, informações do sistema e gravações.
//
// Tudo aqui só responde à página local (mesma origem). As decisões de política (perfis, ACL, modo de
// aceite) ficam na UI do host; este arquivo guarda os segredos e valida provas criptográficas.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"
)

// ---------- preferências tipadas (o núcleo lê do mesmo mapa que a UI grava) ----------

func prefStr(p map[string]any, k, def string) string {
	if v, ok := p[k].(string); ok && v != "" {
		return v
	}
	return def
}
func prefBool(p map[string]any, k string, def bool) bool {
	if v, ok := p[k].(bool); ok {
		return v
	}
	return def
}
func prefNum(p map[string]any, k string, def float64) float64 {
	if v, ok := p[k].(float64); ok {
		return v
	}
	return def
}

// ---------- TOTP (RFC 6238: SHA-1, 6 dígitos, 30 s) ----------

func totpAt(secret []byte, step int64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(b[:])
	s := m.Sum(nil)
	o := s[len(s)-1] & 0x0f
	n := (uint32(s[o]&0x7f)<<24 | uint32(s[o+1])<<16 | uint32(s[o+2])<<8 | uint32(s[o+3])) % 1_000_000
	return fmt.Sprintf("%06d", n)
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// checkTOTP aceita o passo atual e os vizinhos (±30 s) e nunca aceita o mesmo passo duas vezes.
func checkTOTP(secretB32, code string, last *int64) bool {
	secret, err := b32.DecodeString(strings.ToUpper(secretB32))
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if err != nil || len(code) != 6 {
		return false
	}
	now := time.Now().Unix() / 30
	for d := int64(-1); d <= 1; d++ {
		st := now + d
		if st > *last && subtle.ConstantTimeCompare([]byte(totpAt(secret, st)), []byte(code)) == 1 {
			*last = st
			return true
		}
	}
	return false
}

// ---------- tokens ----------

type tokenRec struct {
	ID      string `json:"id"`
	Secret  string `json:"secret"` // hex; é a chave do HMAC, nunca sai do host além da emissão
	For     string `json:"for,omitempty"`
	Via     string `json:"via"` // "fixed" | "temp": como a sessão de origem foi autorizada
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
	Short   bool   `json:"short,omitempty"`
	UsedAt  int64  `json:"usedAt,omitempty"` // curto prazo: uso único
}

func macHex(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func macEq(key []byte, msg, proofHex string) bool {
	want := hmac.New(sha256.New, key)
	want.Write([]byte(msg))
	got, err := hex.DecodeString(proofHex)
	return err == nil && hmac.Equal(want.Sum(nil), got)
}

var (
	recName  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}\.webm$`)
	weakPass = map[string]bool{"12345678": true, "123456789": true, "1234567890": true, "password": true, "senha123": true, "qwertyui": true, "11111111": true, "00000000": true, "abcd1234": true, "12341234": true}
)

// passwordProblem devolve o motivo de uma senha fixa ser fraca, ou "".
func passwordProblem(pw string) string {
	if len([]rune(pw)) < 8 {
		return "A senha fixa precisa ter pelo menos 8 caracteres"
	}
	if weakPass[strings.ToLower(pw)] {
		return "Essa senha é muito comum. Escolha outra"
	}
	first := []rune(pw)[0]
	same := true
	for _, r := range pw {
		if r != first {
			same = false
		}
	}
	if same {
		return "A senha não pode ter só um caractere repetido"
	}
	return ""
}

type security struct {
	mu       sync.Mutex
	pending  string // segredo 2FA em configuração (ainda não ativado)
	lastStep int64
	win      time.Time
	count    int
}

// allow limita as tentativas de autenticação (todas as rotas de prova) a 30 por minuto.
func (s *security) allow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.win) > time.Minute {
		s.win, s.count = time.Now(), 0
	}
	s.count++
	return s.count <= 30
}

func registerSecurity(mux *http.ServeMux, st *store, guard func(http.HandlerFunc) http.HandlerFunc,
	decode func(http.ResponseWriter, *http.Request, any) bool, jsonOut func(http.ResponseWriter, any), agentTok string) {
	sec := &security{}

	prune := func(c *config) {
		now := time.Now().Unix()
		keep := c.Tokens[:0]
		for _, t := range c.Tokens {
			if t.Expires > now && !(t.UsedAt != 0 && now-t.UsedAt > 300) {
				keep = append(keep, t)
			}
		}
		c.Tokens = keep
	}
	// keyFor devolve a chave HMAC de um método de autenticação (fixed | token).
	keyFor := func(kind, tid string, forMAC bool) ([]byte, *tokenRec, bool) {
		c := st.get()
		switch kind {
		case "fixed":
			if c.PassHash == "" {
				return nil, nil, false
			}
			k, err := hex.DecodeString(c.PassHash)
			return k, nil, err == nil
		case "token":
			now := time.Now().Unix()
			for i := range c.Tokens {
				t := c.Tokens[i]
				if t.ID != tid || t.Expires <= now {
					continue
				}
				if t.UsedAt != 0 && (!forMAC || now-t.UsedAt > 120) {
					return nil, nil, false
				}
				k, err := hex.DecodeString(t.Secret)
				return k, &t, err == nil
			}
		}
		return nil, nil, false
	}

	// Dados públicos do desafio: o sal da senha fixa e se há 2FA configurado.
	mux.HandleFunc("/api/auth/info", guard(func(w http.ResponseWriter, r *http.Request) {
		c := st.get()
		jsonOut(w, map[string]any{"salt": c.PassSalt, "iters": pbkdf2Iter, "has": c.PassHash != "", "tfa": c.TFASecret != ""})
	}))

	mux.HandleFunc("/api/auth/verify", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Kind, TID, Ctx, Proof string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		if !sec.allow() {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		key, tok, ok := keyFor(in.Kind, in.TID, false)
		if !ok || !macEq(key, in.Ctx, in.Proof) {
			jsonOut(w, map[string]any{"ok": false})
			return
		}
		out := map[string]any{"ok": true, "via": "fixed"}
		if tok != nil {
			out["via"], out["short"] = tok.Via, tok.Short
			if tok.Short {
				st.update(func(c *config) {
					for i := range c.Tokens {
						if c.Tokens[i].ID == in.TID {
							c.Tokens[i].UsedAt = time.Now().Unix()
						}
					}
				})
			}
		}
		jsonOut(w, out)
	}))

	// MAC das mensagens de oferta/resposta, ligado ao fingerprint DTLS (bloqueia troca do SDP no servidor).
	mux.HandleFunc("/api/auth/mac", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Kind, TID, Ctx, Msg string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		key, _, ok := keyFor(in.Kind, in.TID, true)
		if !ok {
			http.Error(w, "unknown credential", http.StatusForbidden)
			return
		}
		jsonOut(w, map[string]string{"mac": macHex(key, in.Ctx+"|"+in.Msg)})
	}))

	// ---- tokens de acesso salvo / curto prazo ----
	mux.HandleFunc("/api/token", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // excluir todos os dados de acesso salvos
			st.update(func(c *config) { c.Tokens = nil })
		}
		c := st.get()
		n := 0
		for _, t := range c.Tokens {
			if !t.Short {
				n++
			}
		}
		jsonOut(w, map[string]int{"n": n})
	}))
	mux.HandleFunc("/api/token/issue", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			For, Via string
			Short    bool
		}
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		p := st.get().Prefs
		if (in.Short && !prefBool(p, "allowShortTerm", false)) || (!in.Short && !prefBool(p, "allowSave", true)) {
			http.Error(w, "not allowed", http.StatusForbidden)
			return
		}
		if in.Via != "fixed" {
			in.Via = "temp"
		}
		raw := make([]byte, 32)
		rand.Read(raw)
		sum := sha256.Sum256(raw)
		now := time.Now()
		life := 90 * 24 * time.Hour
		if in.Short {
			life = 15 * time.Minute
		}
		t := tokenRec{ID: hex.EncodeToString(sum[:8]), Secret: hex.EncodeToString(raw), For: in.For, Via: in.Via,
			Created: now.Unix(), Expires: now.Add(life).Unix(), Short: in.Short}
		st.update(func(c *config) {
			prune(c)
			c.Tokens = append(c.Tokens, t)
			if len(c.Tokens) > 50 {
				c.Tokens = c.Tokens[len(c.Tokens)-50:]
			}
		})
		jsonOut(w, map[string]any{"id": t.ID, "secret": t.Secret, "expires": t.Expires})
	}))

	// ---- 2FA ----
	mux.HandleFunc("/api/2fa", guard(func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, map[string]bool{"enabled": st.get().TFASecret != ""})
	}))
	mux.HandleFunc("/api/2fa/setup", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		raw := make([]byte, 20)
		rand.Read(raw)
		secret := b32.EncodeToString(raw)
		sec.mu.Lock()
		sec.pending = secret
		sec.mu.Unlock()
		label := "VeloDesk:" + st.get().ID
		jsonOut(w, map[string]string{"secret": secret, "uri": "otpauth://totp/" + url.PathEscape(label) + "?secret=" + secret + "&issuer=VeloDesk&digits=6&period=30"})
	}))
	mux.HandleFunc("/api/2fa/qr", guard(func(w http.ResponseWriter, r *http.Request) {
		sec.mu.Lock()
		secret := sec.pending
		sec.mu.Unlock()
		if secret == "" {
			http.NotFound(w, r)
			return
		}
		label := "VeloDesk:" + st.get().ID
		uri := "otpauth://totp/" + url.PathEscape(label) + "?secret=" + secret + "&issuer=VeloDesk&digits=6&period=30"
		code, err := qr.Encode(uri, qr.M)
		if err != nil {
			http.Error(w, "qr", http.StatusInternalServerError)
			return
		}
		code.Scale = 6
		w.Header().Set("Content-Type", "image/png")
		w.Write(code.PNG())
	}))
	mux.HandleFunc("/api/2fa/enable", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		if !sec.allow() {
			http.Error(w, "too many attempts", http.StatusTooManyRequests)
			return
		}
		sec.mu.Lock()
		secret := sec.pending
		ok := secret != "" && checkTOTP(secret, in.Code, &sec.lastStep)
		if ok {
			sec.pending = ""
		}
		sec.mu.Unlock()
		if ok {
			st.update(func(c *config) { c.TFASecret = secret })
		}
		jsonOut(w, map[string]bool{"ok": ok})
	}))
	check2fa := func(code string) bool {
		secret := st.get().TFASecret
		if secret == "" {
			return false
		}
		sec.mu.Lock()
		defer sec.mu.Unlock()
		return checkTOTP(secret, code, &sec.lastStep)
	}
	mux.HandleFunc("/api/2fa/disable", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		ok := sec.allow() && check2fa(in.Code)
		if ok {
			st.update(func(c *config) { c.TFASecret = "" })
		}
		jsonOut(w, map[string]bool{"ok": ok})
	}))
	mux.HandleFunc("/api/2fa/verify", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		jsonOut(w, map[string]bool{"ok": sec.allow() && check2fa(in.Code)})
	}))

	// ---- ações remotas (exigem o token do agente, como a injeção de entrada) ----
	mux.HandleFunc("/api/action", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ A string }
		if r.Method != http.MethodPost || !decode(w, r, &in) {
			return
		}
		if agentTok == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Agent-Token")), []byte(agentTok)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		cmd := actionCmd(in.A)
		if cmd == nil {
			http.Error(w, "ação não suportada neste sistema", http.StatusNotImplemented)
			return
		}
		if err := cmd.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		go cmd.Wait()
		jsonOut(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/sysinfo", guard(func(w http.ResponseWriter, r *http.Request) {
		host, _ := os.Hostname()
		name := ""
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
		jsonOut(w, map[string]any{"host": host, "user": name, "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(), "memMB": totalMemMB(), "version": version})
	}))

	// ---- gravações ----
	recDir := func() string {
		if d := prefStr(st.get().Prefs, "recDir", ""); d != "" {
			return d
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "VeloDesk", "Gravacoes")
	}
	mux.HandleFunc("/api/rec/dir", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in struct{ Dir string }
			if !decode(w, r, &in) {
				return
			}
			d := strings.TrimSpace(in.Dir)
			if d != "" {
				if !filepath.IsAbs(d) {
					http.Error(w, "Use um caminho completo (ex.: C:\\Gravacoes)", http.StatusBadRequest)
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
		jsonOut(w, map[string]string{"dir": recDir()})
	}))
	mux.HandleFunc("/api/rec/chunk", guard(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if r.Method != http.MethodPost || !recName.MatchString(name) || !prefBool(st.get().Prefs, "recEnable", true) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		dir := recDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		if _, err := io.Copy(f, http.MaxBytesReader(w, r.Body, 64<<20)); err != nil {
			http.Error(w, "write", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("/api/rec/list", guard(func(w http.ResponseWriter, r *http.Request) {
		type item struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			Mod  int64  `json:"mod"`
		}
		out := []item{}
		ents, _ := os.ReadDir(recDir())
		for _, e := range ents {
			if fi, err := e.Info(); err == nil && !e.IsDir() && recName.MatchString(e.Name()) {
				out = append(out, item{e.Name(), fi.Size(), fi.ModTime().Unix()})
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Mod > out[j].Mod })
		jsonOut(w, out)
	}))
	mux.HandleFunc("/api/rec/delete", guard(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name string }
		if r.Method != http.MethodPost || !decode(w, r, &in) || !recName.MatchString(in.Name) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		os.Remove(filepath.Join(recDir(), in.Name))
		jsonOut(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/rec/open", guard(func(w http.ResponseWriter, r *http.Request) {
		dir := recDir()
		os.MkdirAll(dir, 0o700)
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("explorer", dir)
		case "darwin":
			cmd = exec.Command("open", dir)
		default:
			cmd = exec.Command("xdg-open", dir)
		}
		cmd.Start()
		jsonOut(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/rec/", guard(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/rec/")
		if !recName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(recDir(), name))
	}))
}

// actionCmd monta o comando de cada ação remota; nil quando o sistema não suporta.
func actionCmd(a string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		switch a {
		case "restart":
			return exec.Command("shutdown", "/r", "/t", "5", "/c", "VeloDesk: reinício solicitado remotamente")
		case "lock":
			return exec.Command("rundll32.exe", "user32.dll,LockWorkStation")
		case "logoff":
			return exec.Command("shutdown", "/l")
		}
	case "linux":
		switch a {
		case "restart":
			return exec.Command("systemctl", "reboot")
		case "lock":
			return exec.Command("loginctl", "lock-session")
		case "logoff":
			return exec.Command("loginctl", "terminate-user", os.Getenv("USER"))
		}
	}
	return nil
}
