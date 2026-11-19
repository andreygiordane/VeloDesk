// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

// Package agent injects mouse and keyboard events into the local desktop.
// Linux uses X11 (XTest), Windows uses SendInput. Original code.
//
// Access is guarded by a token that is only handed to pages served from this
// same machine, and by the WebSocket same-origin check, so a random web page
// cannot drive the computer.
package agent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type event struct {
	K    string  `json:"k"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	B    int     `json:"b"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	Code string  `json:"code"`
	Tok  string  `json:"token"`
}

// backend is the per-OS input implementation. Coordinates are normalized (0..1).
type backend interface {
	Move(nx, ny float64)
	Button(b int, down bool) // b: JS MouseEvent.button (0 left, 1 middle, 2 right)
	Key(code string, down bool)
	Wheel(dx, dy float64) // browser deltas in pixels (positive dy = scroll down)
	ReleaseAll()
	Size() (int, int)
	// SetRegion aponta o mouse para um monitor (retângulo em pixels da área de trabalho virtual). w<=0 volta ao monitor principal.
	SetRegion(x, y, w, h int)
}

// Injector drives the local desktop.
type Injector struct{ backend backend }

// New starts input injection for this OS. It fails when unavailable
// (for example, no X11 session on Linux, or an OS that is not supported yet).
func New() (*Injector, error) {
	b, err := newBackend()
	if err != nil {
		return nil, err
	}
	return &Injector{backend: b}, nil
}

// Size returns the primary screen size in pixels.
func (in *Injector) Size() (int, int) { return in.backend.Size() }

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (in *Injector) handle(e event) {
	b := in.backend
	switch e.K {
	case "mm":
		b.Move(clamp(e.X), clamp(e.Y))
	case "md":
		b.Move(clamp(e.X), clamp(e.Y))
		b.Button(e.B, true)
	case "mu":
		b.Move(clamp(e.X), clamp(e.Y))
		b.Button(e.B, false)
	case "reg":
		b.SetRegion(int(e.X), int(e.Y), int(e.DX), int(e.DY))
	case "wh":
		b.Wheel(e.DX, e.DY)
	case "kd":
		b.Key(e.Code, true)
	case "ku":
		b.Key(e.Code, false)
	}
}

// Token returns the persistent pairing token, creating it on first use.
func Token() string {
	dir, _ := os.UserConfigDir()
	path := filepath.Join(dir, "velodesk", "agent-token")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		return strings.TrimSpace(string(b))
	}
	raw := make([]byte, 16)
	rand.Read(raw)
	tok := hex.EncodeToString(raw)
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		log.Printf("agent: could not save token: %v", err)
	}
	return tok
}

// Handler serves the input WebSocket. The first message must be {"k":"auth","token":...}.
func (in *Injector) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil) // default: Origin must match Host
		if err != nil {
			return
		}
		defer c.CloseNow()
		c.SetReadLimit(4 << 10)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer in.backend.ReleaseAll()

		actx, acancel := context.WithTimeout(ctx, 5*time.Second)
		_, b, err := c.Read(actx)
		acancel()
		var first event
		if err != nil || json.Unmarshal(b, &first) != nil || first.K != "auth" ||
			subtle.ConstantTimeCompare([]byte(first.Tok), []byte(token)) != 1 {
			c.Close(websocket.StatusPolicyViolation, "bad token")
			log.Printf("agent: rejected connection from %s", r.RemoteAddr)
			return
		}
		c.Write(ctx, websocket.MessageText, []byte(`{"k":"ready"}`))
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var e event
			if json.Unmarshal(b, &e) == nil {
				in.handle(e)
			}
		}
	})
}
