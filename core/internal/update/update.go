// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

// Package update implements signed auto-updates. The server only hosts files; the client
// accepts a build only if its Ed25519 signature (made with the publisher's private key,
// verified against the public key embedded at build time) checks out. A hostile network or
// server therefore cannot push code to clients.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed update.pub
var pubB64 string

// Manifest is published by `velodesk publish` as manifest.json next to the binaries.
type Manifest struct {
	Version   string          `json:"version"`
	Notes     string          `json:"notes,omitempty"`
	Published string          `json:"published,omitempty"`
	Files     map[string]File `json:"files"` // key: "<goos>-<goarch>"
}

type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Sig    string `json:"sig"` // base64 Ed25519 over SigMessage
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}$`)

// Platform is the key used in the manifest for this build.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// SigMessage is what gets signed. It binds version, platform and content hash, so a
// valid signature cannot be replayed as another version or platform.
func SigMessage(version, platform, sha string) []byte {
	return []byte("velodesk-update\n" + version + "\n" + platform + "\n" + sha + "\n")
}

func publicKey() (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("no update public key embedded in this build")
	}
	return ed25519.PublicKey(b), nil
}

// Enabled reports whether this build can verify updates.
func Enabled() bool { _, err := publicKey(); return err == nil }

// Verify checks the signature of the entry for this platform.
func Verify(m Manifest, platform string) (File, error) {
	pub, err := publicKey()
	if err != nil {
		return File{}, err
	}
	f, ok := m.Files[platform]
	if !ok {
		return File{}, fmt.Errorf("no build for %s", platform)
	}
	sig, err := base64.StdEncoding.DecodeString(f.Sig)
	if err != nil || !ed25519.Verify(pub, SigMessage(m.Version, platform, f.SHA256), sig) {
		return File{}, errors.New("invalid update signature")
	}
	if !safeName.MatchString(f.Name) || len(f.SHA256) != 64 {
		return File{}, errors.New("invalid update entry")
	}
	return f, nil
}

func parse(v string) []int {
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(v, "v"), ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// Newer reports whether a is a higher version than b.
func Newer(a, b string) bool {
	x, y := parse(a), parse(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			return p > q
		}
	}
	return false
}

// Dir is where verified updates are stored (the desktop shell launches the newest one).
func Dir() string {
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "velodesk", "updates")
}

func localName(version string) string {
	if runtime.GOOS == "windows" {
		return "velodesk-" + version + ".exe"
	}
	return "velodesk-" + version
}

// Status is exposed to the UI.
type Status struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	State   string `json:"state"` // disabled | off | idle | checking | downloading | ready | error
	Notes   string `json:"notes,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Manager struct {
	current  string
	endpoint func() (base string, insecure bool)
	client   func(insecure bool) *http.Client
	trigger  chan struct{}
	mode     func() string // "off" | "stable" | "beta"
	mu       sync.Mutex
	st       Status
}

func NewManager(current string, endpoint func() (string, bool), client func(bool) *http.Client) *Manager {
	m := &Manager{current: current, endpoint: endpoint, client: client, trigger: make(chan struct{}, 1)}
	m.st = Status{Current: current, Latest: current, State: "idle"}
	if current == "dev" || !Enabled() {
		m.st.State = "disabled"
	}
	return m
}

// SetMode informa o canal de atualização escolhido pelo usuário (lido a cada verificação).
func (m *Manager) SetMode(f func() string) { m.mode = f }

func (m *Manager) Status() Status      { m.mu.Lock(); defer m.mu.Unlock(); return m.st }
func (m *Manager) set(f func(*Status)) { m.mu.Lock(); f(&m.st); m.mu.Unlock() }

// Trigger asks for a check now (called on a server push or from the UI).
func (m *Manager) Trigger() {
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

// Run checks shortly after start, every 15 minutes (safety net), and immediately on a server push.
func (m *Manager) Run(ctx context.Context) {
	if m.Status().State == "disabled" {
		return
	}
	t := time.NewTimer(5 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.trigger:
		}
		m.check(ctx)
		t.Reset(15 * time.Minute)
	}
}

func (m *Manager) fail(err error) { m.set(func(s *Status) { s.State, s.Error = "error", err.Error() }) }

func (m *Manager) check(ctx context.Context) {
	base, insecure := m.endpoint()
	if base == "" {
		return
	}
	mode := "stable"
	if m.mode != nil {
		mode = m.mode()
	}
	if mode == "off" {
		m.set(func(s *Status) { s.State, s.Error = "off", "" })
		return
	}
	m.set(func(s *Status) { s.State, s.Error = "checking", "" })
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	names := []string{"manifest.json"}
	if mode == "beta" {
		names = []string{"manifest-beta.json", "manifest.json"} // beta cai para o estável se não houver beta
	}
	var resp *http.Response
	var err error
	for _, n := range names {
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, base+"/"+n, nil)
		if resp, err = m.client(insecure).Do(req); err != nil {
			m.fail(err)
			return
		}
		if resp.StatusCode != http.StatusNotFound {
			break
		}
		resp.Body.Close()
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		m.set(func(s *Status) { s.State = "idle" }) // server publishes no updates
		return
	}
	if resp.StatusCode != 200 {
		m.fail(fmt.Errorf("manifest: HTTP %d", resp.StatusCode))
		return
	}
	var man Manifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&man); err != nil {
		m.fail(err)
		return
	}
	f, err := Verify(man, Platform())
	if err != nil {
		m.fail(err)
		return
	}
	if !Newer(man.Version, m.current) { // also blocks rollbacks
		m.set(func(s *Status) { s.State, s.Latest = "idle", m.current })
		return
	}
	dst := filepath.Join(Dir(), localName(man.Version))
	if _, err := os.Stat(dst); err == nil {
		m.set(func(s *Status) { s.State, s.Latest, s.Notes = "ready", man.Version, man.Notes })
		return
	}
	m.set(func(s *Status) { s.State, s.Latest, s.Notes = "downloading", man.Version, man.Notes })
	if err := m.download(ctx, base, f, dst, insecure); err != nil {
		m.fail(err)
		return
	}
	prune(man.Version)
	m.set(func(s *Status) { s.State = "ready" })
}

func (m *Manager) download(ctx context.Context, base string, f File, dst string, insecure bool) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, base+"/"+f.Name, nil)
	resp, err := m.client(insecure).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "dl-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	const max = 300 << 20
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, max+1))
	tmp.Close()
	if err != nil {
		return err
	}
	if n > max {
		return errors.New("update too large")
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(f.SHA256) {
		return errors.New("update hash mismatch (corrupted or tampered download)")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// prune keeps the newest two downloaded versions.
func prune(keep string) {
	ents, _ := os.ReadDir(Dir())
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "velodesk-") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return Newer(strings.TrimSuffix(strings.TrimPrefix(names[i], "velodesk-"), ".exe"), strings.TrimSuffix(strings.TrimPrefix(names[j], "velodesk-"), ".exe"))
	})
	for i, n := range names {
		if i >= 2 {
			os.Remove(filepath.Join(Dir(), n))
		}
	}
}
