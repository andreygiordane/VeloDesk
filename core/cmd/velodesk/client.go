// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

package main

// VeloDesk client app: this is what runs on every machine. It
//   - serves the UI on loopback only (http://127.0.0.1:21116, a secure context
//     for the browser, no certificate needed),
//   - keeps a stable device ID,
//   - connects to the signaling server on behalf of the page (so TLS problems
//     and mixed-content never reach the browser),
//   - runs the local input agent so this machine can be controlled.

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"velodesk/internal/agent"
	"velodesk/internal/update"

	"github.com/coder/websocket"
)

//go:embed ui
var uiFS embed.FS

type config struct {
	Server    string         `json:"server"`
	ID        string         `json:"id"`
	Insecure  bool           `json:"insecure"`
	Prefs     map[string]any `json:"prefs,omitempty"`
	PassSalt  string         `json:"passSalt,omitempty"`
	PassHash  string         `json:"passHash,omitempty"`
	TFASecret string         `json:"tfaSecret,omitempty"`
	Tokens    []tokenRec     `json:"tokens,omitempty"`
}

const pbkdf2Iter = 200_000

func hashPassword(pw string, salt []byte) string {
	k, _ := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	return hex.EncodeToString(k)
}

type store struct {
	mu       sync.Mutex
	path     string
	c        config
	onUpdate func() // server announced a new release
}

func loadStore() *store {
	dir, _ := os.UserConfigDir()
	s := &store{path: filepath.Join(dir, "velodesk", "config.json")}
	if b, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(b, &s.c)
	}
	return s
}

func (s *store) get() config { s.mu.Lock(); defer s.mu.Unlock(); return s.c }

func (s *store) update(f func(*config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.c)
	b, _ := json.MarshalIndent(s.c, "", "  ")
	os.MkdirAll(filepath.Dir(s.path), 0o700)
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		log.Printf("could not save config: %v", err)
	}
}

// embeddedServer devolve o servidor embutido no instalador (default-server.txt ao lado do executável, ou
// VELODESK_DEFAULT_SERVER) e se ele é fixo: com server-locked.txt (ou VELODESK_LOCK_SERVER=1) o app só usa esse servidor.
func embeddedServer() (def string, locked bool) {
	def = strings.TrimSpace(os.Getenv("VELODESK_DEFAULT_SERVER"))
	locked = os.Getenv("VELODESK_LOCK_SERVER") == "1"
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if def == "" {
			if b, err := os.ReadFile(filepath.Join(dir, "default-server.txt")); err == nil {
				def = strings.TrimSpace(string(b))
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "server-locked.txt")); err == nil {
			locked = true
		}
	}
	return def, locked && def != ""
}

// normalizeServer turns "192.168.1.5:21116", "https://host" etc. into a ws(s):// URL ending in /ws.
func normalizeServer(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", nil
	}
	if !strings.Contains(in, "://") {
		in = "wss://" + in
	}
	u, err := url.Parse(in)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("endereço inválido")
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("use ws:// ou wss://")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	return u.String(), nil
}

type pmsg struct {
	T    string          `json:"t"`
	ID   string          `json:"id,omitempty"`
	To   string          `json:"to,omitempty"`
	From string          `json:"from,omitempty"`
	Msg  string          `json:"msg,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// sameOrigin rejects requests that did not come from our own page.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	}
	return true
}

func (s *store) wsProxy(w http.ResponseWriter, r *http.Request) {
	page, err := websocket.Accept(w, r, nil) // same-origin enforced by the library
	if err != nil {
		return
	}
	defer page.CloseNow()
	page.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	fail := func(code string) {
		b, _ := json.Marshal(pmsg{T: "error", Msg: code})
		page.Write(ctx, websocket.MessageText, b)
		page.Close(websocket.StatusNormalClosure, code)
	}
	cfg := s.get()
	if cfg.Server == "" {
		fail("no_server")
		return
	}
	dctx, dcancel := context.WithTimeout(ctx, 8*time.Second)
	opts := &websocket.DialOptions{HTTPClient: httpClientFor(cfg.Prefs, cfg.Insecure)}
	up, _, err := websocket.Dial(dctx, cfg.Server, opts)
	dcancel()
	if err != nil {
		log.Printf("signaling server unreachable (%s): %v", cfg.Server, err)
		fail("server_unreachable")
		return
	}
	defer up.CloseNow()
	up.SetReadLimit(64 << 10)
	hello, _ := json.Marshal(pmsg{T: "hello", ID: cfg.ID})
	if up.Write(ctx, websocket.MessageText, hello) != nil {
		fail("server_unreachable")
		return
	}

	done := make(chan struct{}, 2)
	go func() { // server -> page
		defer func() { done <- struct{}{} }()
		for {
			typ, b, err := up.Read(ctx)
			if err != nil {
				return
			}
			var m pmsg
			if json.Unmarshal(b, &m) == nil && m.T == "update" && s.onUpdate != nil {
				s.onUpdate()
				continue
			}
			if json.Unmarshal(b, &m) == nil && m.T == "welcome" && m.ID != s.get().ID {
				id := m.ID
				s.update(func(c *config) { c.ID = id })
			}
			if page.Write(ctx, typ, b) != nil {
				return
			}
		}
	}()
	go func() { // page -> server (only signaling messages)
		defer func() { done <- struct{}{} }()
		for {
			typ, b, err := page.Read(ctx)
			if err != nil {
				return
			}
			var m pmsg
			if json.Unmarshal(b, &m) != nil || (m.T != "signal" && m.T != "presence") {
				continue
			}
			if up.Write(ctx, typ, b) != nil {
				return
			}
		}
	}()
	<-done
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	cmd.Start()
}

func runClient(args []string) {
	fl := flag.NewFlagSet("velodesk", flag.ExitOnError)
	addr := fl.String("addr", "127.0.0.1:21120", "UI address (keep it on loopback)")
	serverFlag := fl.String("server", "", "signaling server to use (saved), e.g. wss://remote.example.com")
	noOpen := fl.Bool("no-open", false, "do not open the browser")
	noAgent := fl.Bool("no-agent", false, "do not start the local input agent")
	fl.Parse(args)

	host, _, _ := net.SplitHostPort(*addr)
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		log.Fatalf("refusing to serve the UI on %q: use a loopback address", *addr)
	}

	st := loadStore()
	def, locked := embeddedServer()
	if *serverFlag != "" && !locked {
		u, err := normalizeServer(*serverFlag)
		if err != nil {
			log.Fatal(err)
		}
		st.update(func(c *config) { c.Server = u })
	}

	// First run: use the server baked into the installer (default-server.txt next to the
	// executable) or VELODESK_DEFAULT_SERVER, so a fresh install connects without setup.
	if locked { // servidor fixo do instalador: sempre ele, mesmo que haja outro salvo de antes
		if u, err := normalizeServer(def); err == nil && u != "" {
			st.update(func(c *config) { c.Server, c.Insecure = u, false })
		}
	} else if st.get().Server == "" {
		if u, err := normalizeServer(def); err == nil && u != "" {
			st.update(func(c *config) { c.Server = u })
		}
	}

	// ---- signed auto-update ----
	httpClient := func(insecure bool) *http.Client { return httpClientFor(st.get().Prefs, insecure) }
	um := update.NewManager(version, func() (string, bool) {
		c := st.get()
		return updateBase(c.Server), c.Insecure
	}, httpClient)
	um.SetMode(func() string { return prefStr(st.get().Prefs, "updateMode", "stable") })
	st.onUpdate = um.Trigger
	go um.Run(context.Background())

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", st.wsProxy)
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !sameOrigin(r) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			h(w, r)
		}
	}
	jsonOut := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(v) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return false
		}
		return true
	}
	view := func() map[string]any {
		c := st.get()
		srv, ins := c.Server, c.Insecure
		if locked { // o endereço do servidor embutido não aparece em lugar nenhum da interface
			srv, ins = "", false
		}
		return map[string]any{"server": srv, "locked": locked, "id": c.ID, "insecure": ins, "hasPassword": c.PassHash != "", "hasTFA": c.TFASecret != ""}
	}
	mux.HandleFunc("/api/config", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !locked {
			var in struct {
				Server   string `json:"server"`
				Insecure bool   `json:"insecure"`
			}
			if !decode(w, r, &in) {
				return
			}
			u, err := normalizeServer(in.Server)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			st.update(func(c *config) { c.Server = u; c.Insecure = in.Insecure })
		}
		jsonOut(w, view())
	}))
	mux.HandleFunc("/api/update", guard(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Get("do") == "check":
			um.Trigger()
		case r.Method == http.MethodPost && r.URL.Query().Get("do") == "apply":
			if um.Status().State != "ready" || os.Getenv("VELODESK_SUPERVISED") != "1" {
				http.Error(w, "no update ready, or the app is not supervised by the desktop shell", http.StatusConflict)
				return
			}
			jsonOut(w, map[string]bool{"restarting": true})
			go func() { time.Sleep(400 * time.Millisecond); log.Printf("restarting to apply update"); os.Exit(42) }()
			return
		}
		jsonOut(w, struct {
			update.Status
			Supervised bool `json:"supervised"` // only the desktop shell restarts the core after an update
		}{um.Status(), os.Getenv("VELODESK_SUPERVISED") == "1"})
	}))
	// Preferences: flat map of string/number/bool values, persisted by the core.
	mux.HandleFunc("/api/prefs", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var in map[string]any
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in) != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if len(in) > 256 {
				http.Error(w, "too many keys", http.StatusBadRequest)
				return
			}
			for k, v := range in {
				switch x := v.(type) {
				case string:
					if len(x) > 32<<10 {
						http.Error(w, "value too long: "+k, http.StatusBadRequest)
						return
					}
				case float64, bool:
				default:
					http.Error(w, "invalid value for "+k, http.StatusBadRequest)
					return
				}
			}
			st.update(func(c *config) { c.Prefs = in })
		}
		p := st.get().Prefs
		if p == nil {
			p = map[string]any{}
		}
		jsonOut(w, p)
	}))
	// Permanent (unattended) password: stored only as a salted PBKDF2 hash.
	mux.HandleFunc("/api/password", guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Password string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Password == "" {
			st.update(func(c *config) { c.PassSalt, c.PassHash = "", "" })
		} else {
			if msg := passwordProblem(in.Password); msg != "" {
				http.Error(w, msg, http.StatusBadRequest)
				return
			}
			salt := make([]byte, 16)
			rand.Read(salt)
			h := hashPassword(in.Password, salt)
			st.update(func(c *config) { c.PassSalt, c.PassHash = hex.EncodeToString(salt), h })
		}
		jsonOut(w, view())
	}))
	agentTok := ""
	if !*noAgent {
		if in, err := agent.New(); err != nil {
			log.Printf("remote control disabled (needs an X11 session): %v", err)
		} else {
			tok := agent.Token()
			agentTok = tok
			w, h := in.Size()
			log.Printf("input agent ready (screen %dx%d)", w, h)
			mux.Handle("/agent", in.Handler(tok))
			mux.HandleFunc("/agent-token", func(rw http.ResponseWriter, r *http.Request) {
				if !sameOrigin(r) {
					http.Error(rw, "forbidden", http.StatusForbidden)
					return
				}
				rw.Header().Set("Cache-Control", "no-store")
				rw.Write([]byte(tok))
			})
		}
	}
	dec := func(w http.ResponseWriter, r *http.Request, v any) bool { return decode(w, r, v) }
	registerFiles(mux, st, guard, dec, jsonOut, agentTok)
	registerFS(mux, st, guard, dec, jsonOut, agentTok)
	registerWOL(mux, st, guard, dec, jsonOut)
	registerTunnels(mux, guard, dec, jsonOut, agentTok)
	registerWallpaper(mux, guard, agentTok)
	registerDiscovery(mux, st, startDiscovery(st), guard, jsonOut)
	registerSecurity(mux, st, guard, func(w http.ResponseWriter, r *http.Request, v any) bool { return decode(w, r, v) }, jsonOut, agentTok)
	sub, _ := fs.Sub(uiFS, "ui")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	url := "http://" + *addr
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s (is VeloDesk already running?): %v", *addr, err)
	}
	if locked {
		log.Printf("server: (embutido no instalador)")
	} else if st.get().Server == "" {
		log.Printf("no server configured yet: set it in the app (gear icon) or run: velodesk -server <address>")
	} else {
		log.Printf("server: %s", st.get().Server)
	}
	log.Printf("VeloDesk running at %s", url)
	if !*noOpen {
		openBrowser(url)
	}
	log.Fatal(http.Serve(ln, mux))
}

// updateBase maps the signaling URL (ws://host:port/ws) to the update endpoint (http://host:port/update).
func updateBase(server string) string {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return ""
	}
	u.Path = "/update"
	return u.String()
}
