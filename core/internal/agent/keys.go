// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 VeloDesk contributors

//go:build linux

package agent

import "fmt"

// jsCodeToKeysym maps KeyboardEvent.code (physical key, US layout) to X11 keysyms.
var jsCodeToKeysym = func() map[string]uint32 {
	m := map[string]uint32{
		"Space": 0x20, "Enter": 0xff0d, "Escape": 0xff1b, "Backspace": 0xff08, "Tab": 0xff09,
		"ArrowLeft": 0xff51, "ArrowUp": 0xff52, "ArrowRight": 0xff53, "ArrowDown": 0xff54,
		"Home": 0xff50, "End": 0xff57, "PageUp": 0xff55, "PageDown": 0xff56, "Insert": 0xff63, "Delete": 0xffff,
		"ShiftLeft": 0xffe1, "ShiftRight": 0xffe2, "ControlLeft": 0xffe3, "ControlRight": 0xffe4,
		"CapsLock": 0xffe5, "AltLeft": 0xffe9, "AltRight": 0xffea, "MetaLeft": 0xffeb, "MetaRight": 0xffec,
		"ContextMenu": 0xff67, "PrintScreen": 0xff61, "ScrollLock": 0xff14, "Pause": 0xff13, "NumLock": 0xff7f,
		"Minus": 0x2d, "Equal": 0x3d, "BracketLeft": 0x5b, "BracketRight": 0x5d, "Backslash": 0x5c,
		"Semicolon": 0x3b, "Quote": 0x27, "Backquote": 0x60, "Comma": 0x2c, "Period": 0x2e, "Slash": 0x2f,
		"NumpadEnter": 0xff8d, "NumpadAdd": 0xffab, "NumpadSubtract": 0xffad, "NumpadMultiply": 0xffaa,
		"NumpadDivide": 0xffaf, "NumpadDecimal": 0xffae,
	}
	for c := 'a'; c <= 'z'; c++ {
		m[fmt.Sprintf("Key%c", c-32)] = uint32(c)
	}
	for d := 0; d <= 9; d++ {
		m[fmt.Sprintf("Digit%d", d)] = uint32(0x30 + d)
		m[fmt.Sprintf("Numpad%d", d)] = uint32(0xffb0 + d)
	}
	for f := 1; f <= 12; f++ {
		m[fmt.Sprintf("F%d", f)] = uint32(0xffbd + f)
	}
	return m
}()
