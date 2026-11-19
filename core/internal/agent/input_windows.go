// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build windows

package agent

import (
	"strconv"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procSendInput   = user32.NewProc("SendInput")
	procMetrics     = user32.NewProc("GetSystemMetrics")
	procDPIAware    = user32.NewProc("SetProcessDPIAware")
	inputSizeNative = unsafe.Sizeof(mouseInput{})
)

// INPUT is 40 bytes on 64-bit Windows; both variants must have exactly that size or SendInput fails silently.
var _ = [1]struct{}{}[unsafe.Sizeof(kbInput{})-unsafe.Sizeof(mouseInput{})]
var _ = [1]struct{}{}[40-unsafe.Sizeof(mouseInput{})]

const (
	inputMouse    = 0
	inputKeyboard = 1

	mevMove        = 0x0001
	mevLeftDown    = 0x0002
	mevLeftUp      = 0x0004
	mevRightDown   = 0x0008
	mevRightUp     = 0x0010
	mevMiddleDown  = 0x0020
	mevMiddleUp    = 0x0040
	mevWheel       = 0x0800
	mevHWheel      = 0x1000
	mevAbsolute    = 0x8000
	mevVirtualDesk = 0x4000

	kevExtended = 0x0001
	kevKeyUp    = 0x0002
	kevScanCode = 0x0008
)

// Both structs are 40 bytes on 64-bit Windows (4 type + 4 pad + 32 union) and must match INPUT exactly.
type mouseInput struct {
	typ   uint32
	_     uint32 // union is 8-aligned
	dx    int32
	dy    int32
	data  uint32
	flags uint32
	time  uint32
	extra uintptr
}

type kbInput struct {
	typ   uint32
	_     uint32 // union is 8-aligned
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte
}

type winKey struct {
	scan uint16
	ext  bool
}

// jsCodeToScan maps KeyboardEvent.code (physical key) to Set-1 scan codes.
var jsCodeToScan = func() map[string]winKey {
	m := map[string]winKey{
		"KeyQ": {0x10, false}, "KeyW": {0x11, false}, "KeyE": {0x12, false}, "KeyR": {0x13, false}, "KeyT": {0x14, false},
		"KeyY": {0x15, false}, "KeyU": {0x16, false}, "KeyI": {0x17, false}, "KeyO": {0x18, false}, "KeyP": {0x19, false},
		"KeyA": {0x1E, false}, "KeyS": {0x1F, false}, "KeyD": {0x20, false}, "KeyF": {0x21, false}, "KeyG": {0x22, false},
		"KeyH": {0x23, false}, "KeyJ": {0x24, false}, "KeyK": {0x25, false}, "KeyL": {0x26, false},
		"KeyZ": {0x2C, false}, "KeyX": {0x2D, false}, "KeyC": {0x2E, false}, "KeyV": {0x2F, false}, "KeyB": {0x30, false},
		"KeyN": {0x31, false}, "KeyM": {0x32, false},
		"Digit1": {0x02, false}, "Digit2": {0x03, false}, "Digit3": {0x04, false}, "Digit4": {0x05, false}, "Digit5": {0x06, false},
		"Digit6": {0x07, false}, "Digit7": {0x08, false}, "Digit8": {0x09, false}, "Digit9": {0x0A, false}, "Digit0": {0x0B, false},
		"Minus": {0x0C, false}, "Equal": {0x0D, false}, "Backspace": {0x0E, false}, "Tab": {0x0F, false},
		"BracketLeft": {0x1A, false}, "BracketRight": {0x1B, false}, "Enter": {0x1C, false}, "ControlLeft": {0x1D, false},
		"Semicolon": {0x27, false}, "Quote": {0x28, false}, "Backquote": {0x29, false}, "ShiftLeft": {0x2A, false},
		"Backslash": {0x2B, false}, "IntlBackslash": {0x56, false}, "Comma": {0x33, false}, "Period": {0x34, false}, "Slash": {0x35, false},
		"ShiftRight": {0x36, false}, "NumpadMultiply": {0x37, false}, "AltLeft": {0x38, false}, "Space": {0x39, false},
		"CapsLock": {0x3A, false}, "NumLock": {0x45, false}, "ScrollLock": {0x46, false},
		"Numpad7": {0x47, false}, "Numpad8": {0x48, false}, "Numpad9": {0x49, false}, "NumpadSubtract": {0x4A, false},
		"Numpad4": {0x4B, false}, "Numpad5": {0x4C, false}, "Numpad6": {0x4D, false}, "NumpadAdd": {0x4E, false},
		"Numpad1": {0x4F, false}, "Numpad2": {0x50, false}, "Numpad3": {0x51, false}, "Numpad0": {0x52, false}, "NumpadDecimal": {0x53, false},
		"F11": {0x57, false}, "F12": {0x58, false}, "Escape": {0x01, false},
		// extended (E0-prefixed) keys
		"NumpadEnter": {0x1C, true}, "ControlRight": {0x1D, true}, "NumpadDivide": {0x35, true}, "PrintScreen": {0x37, true},
		"AltRight": {0x38, true}, "Home": {0x47, true}, "ArrowUp": {0x48, true}, "PageUp": {0x49, true},
		"ArrowLeft": {0x4B, true}, "ArrowRight": {0x4D, true}, "End": {0x4F, true}, "ArrowDown": {0x50, true},
		"PageDown": {0x51, true}, "Insert": {0x52, true}, "Delete": {0x53, true},
		"MetaLeft": {0x5B, true}, "MetaRight": {0x5C, true}, "ContextMenu": {0x5D, true},
	}
	for i := 0; i < 10; i++ { // F1..F10
		m["F"+strconv.Itoa(i+1)] = winKey{uint16(0x3B + i), false}
	}
	return m
}()

type win struct {
	w, h      int
	rx, ry    int // monitor escolhido (área virtual); rw=0: monitor principal
	rw, rh    int
	mu        sync.Mutex
	downKeys  map[string]bool
	downBtns  map[int]bool
	wheelAccX float64
	wheelAccY float64
}

func newBackend() (backend, error) {
	procDPIAware.Call() // physical pixels, no DPI virtualization
	w, _, _ := procMetrics.Call(0)
	h, _, _ := procMetrics.Call(1)
	return &win{w: int(w), h: int(h), downKeys: map[string]bool{}, downBtns: map[int]bool{}}, nil
}

func (in *win) Size() (int, int) { return in.w, in.h }

func (in *win) mouse(flags uint32, dx, dy int32, data uint32) {
	m := mouseInput{typ: inputMouse, dx: dx, dy: dy, data: data, flags: flags}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&m)), inputSizeNative)
}

func (in *win) SetRegion(x, y, w, h int) {
	in.mu.Lock()
	in.rx, in.ry, in.rw, in.rh = x, y, w, h
	in.mu.Unlock()
}

func (in *win) Move(nx, ny float64) {
	in.mu.Lock()
	rx, ry, rw, rh := in.rx, in.ry, in.rw, in.rh
	in.mu.Unlock()
	if rw > 0 && rh > 0 { // outro monitor: coordenadas absolutas sobre a área de trabalho virtual
		vx, _, _ := procMetrics.Call(76) // SM_XVIRTUALSCREEN
		vy, _, _ := procMetrics.Call(77)
		vw, _, _ := procMetrics.Call(78) // SM_CXVIRTUALSCREEN
		vh, _, _ := procMetrics.Call(79)
		if int32(vw) > 1 && int32(vh) > 1 {
			px := float64(rx) + nx*float64(rw-1)
			py := float64(ry) + ny*float64(rh-1)
			ax := (px - float64(int32(vx))) / float64(int32(vw)-1) * 65535
			ay := (py - float64(int32(vy))) / float64(int32(vh)-1) * 65535
			in.mouse(mevMove|mevAbsolute|mevVirtualDesk, int32(ax), int32(ay), 0)
			return
		}
	}
	in.mouse(mevMove|mevAbsolute, int32(nx*65535), int32(ny*65535), 0)
}

func (in *win) Button(b int, down bool) {
	var f uint32
	switch {
	case b == 1 && down:
		f = mevMiddleDown
	case b == 1:
		f = mevMiddleUp
	case b == 2 && down:
		f = mevRightDown
	case b == 2:
		f = mevRightUp
	case down:
		f = mevLeftDown
	default:
		f = mevLeftUp
	}
	in.mu.Lock()
	if down {
		in.downBtns[b] = true
	} else {
		delete(in.downBtns, b)
	}
	in.mu.Unlock()
	in.mouse(f, 0, 0, 0)
}

func (in *win) Key(code string, down bool) {
	k, ok := jsCodeToScan[code]
	if !ok {
		return
	}
	in.sendKey(k, down)
	in.mu.Lock()
	if down {
		in.downKeys[code] = true
	} else {
		delete(in.downKeys, code)
	}
	in.mu.Unlock()
}

func (in *win) sendKey(k winKey, down bool) {
	flags := uint32(kevScanCode)
	if k.ext {
		flags |= kevExtended
	}
	if !down {
		flags |= kevKeyUp
	}
	i := kbInput{typ: inputKeyboard, scan: k.scan, flags: flags}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&i)), inputSizeNative)
}

// Wheel: browsers report ~100px per notch (positive = down); Windows wants 120 per notch (positive = up).
func (in *win) Wheel(dx, dy float64) {
	in.wheelAccY += -dy * 1.2
	in.wheelAccX += dx * 1.2
	if n := int(in.wheelAccY / 120); n != 0 {
		in.mouse(mevWheel, 0, 0, uint32(int32(n*120)))
		in.wheelAccY -= float64(n * 120)
	}
	if n := int(in.wheelAccX / 120); n != 0 {
		in.mouse(mevHWheel, 0, 0, uint32(int32(n*120)))
		in.wheelAccX -= float64(n * 120)
	}
}

func (in *win) ReleaseAll() {
	in.mu.Lock()
	keys, btns := in.downKeys, in.downBtns
	in.downKeys, in.downBtns = map[string]bool{}, map[int]bool{}
	in.mu.Unlock()
	for c := range keys {
		in.sendKey(jsCodeToScan[c], false)
	}
	for b := range btns {
		in.Button(b, false)
	}
}
