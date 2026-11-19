// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build linux

package agent

import (
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type x11 struct {
	x         *xgb.Conn
	root      xproto.Window
	w, h      uint16
	keycodes  map[uint32]byte
	mu        sync.Mutex
	downKeys  map[byte]bool
	downBtns  map[byte]bool
	wheelAccX float64
	wheelAccY float64
}

func newBackend() (backend, error) {
	x, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	if err := xtest.Init(x); err != nil {
		return nil, err
	}
	setup := xproto.Setup(x)
	scr := setup.DefaultScreen(x)
	in := &x11{x: x, root: scr.Root, w: scr.WidthInPixels, h: scr.HeightInPixels,
		keycodes: map[uint32]byte{}, downKeys: map[byte]bool{}, downBtns: map[byte]bool{}}
	n := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	km, err := xproto.GetKeyboardMapping(x, setup.MinKeycode, n).Reply()
	if err != nil {
		return nil, err
	}
	per := int(km.KeysymsPerKeycode)
	for i := 0; i < int(n); i++ {
		for j := 0; j < per && j < 2; j++ { // unshifted + shifted columns
			ks := uint32(km.Keysyms[i*per+j])
			if ks == 0 {
				continue
			}
			if _, ok := in.keycodes[ks]; !ok {
				in.keycodes[ks] = byte(i) + byte(setup.MinKeycode)
			}
		}
	}
	return in, nil
}

func (in *x11) Size() (int, int) { return int(in.w), int(in.h) }

func (in *x11) fake(typ byte, detail byte, x, y int16) {
	xtest.FakeInput(in.x, typ, detail, 0, in.root, x, y, 0)
}

func (in *x11) Move(nx, ny float64) {
	in.fake(xproto.MotionNotify, 0, int16(nx*float64(in.w-1)), int16(ny*float64(in.h-1)))
}

func (in *x11) Button(b int, down bool) {
	xb := [...]byte{1, 2, 3}[min(max(b, 0), 2)]
	in.mu.Lock()
	defer in.mu.Unlock()
	if down {
		in.fake(xproto.ButtonPress, xb, 0, 0)
		in.downBtns[xb] = true
	} else {
		in.fake(xproto.ButtonRelease, xb, 0, 0)
		delete(in.downBtns, xb)
	}
}

func (in *x11) Key(code string, down bool) {
	ks, ok := jsCodeToKeysym[code]
	if !ok {
		return
	}
	kc, ok := in.keycodes[ks]
	if !ok {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if down {
		in.fake(xproto.KeyPress, kc, 0, 0)
		in.downKeys[kc] = true
	} else {
		in.fake(xproto.KeyRelease, kc, 0, 0)
		delete(in.downKeys, kc)
	}
}

// Wheel converts pixel deltas to X11 scroll "buttons" (4/5 vertical, 6/7 horizontal).
func (in *x11) Wheel(dx, dy float64) {
	const step = 60.0
	in.wheelAccX += dx
	in.wheelAccY += dy
	click := func(b byte) { in.fake(xproto.ButtonPress, b, 0, 0); in.fake(xproto.ButtonRelease, b, 0, 0) }
	for in.wheelAccY >= step {
		click(5)
		in.wheelAccY -= step
	}
	for in.wheelAccY <= -step {
		click(4)
		in.wheelAccY += step
	}
	for in.wheelAccX >= step {
		click(7)
		in.wheelAccX -= step
	}
	for in.wheelAccX <= -step {
		click(6)
		in.wheelAccX += step
	}
}

func (in *x11) ReleaseAll() {
	in.mu.Lock()
	defer in.mu.Unlock()
	for k := range in.downKeys {
		in.fake(xproto.KeyRelease, k, 0, 0)
	}
	for b := range in.downBtns {
		in.fake(xproto.ButtonRelease, b, 0, 0)
	}
	in.downKeys = map[byte]bool{}
	in.downBtns = map[byte]bool{}
}
