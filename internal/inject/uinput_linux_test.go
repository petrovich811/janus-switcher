//go:build linux

package inject

import "testing"

func TestAllowedKeysAreNarrow(t *testing.T) {
	for _, code := range []uint16{KeyDelete, KeyPause, 99 /* Print */, 116 /* Power */, 142 /* Sleep */, 68 /* F10 */} {
		if allowed[code] {
			t.Errorf("key code %d must not be enabled on the Janus keyboard", code)
		}
	}
	for _, code := range []uint16{KeyLeftCtrl, KeyLeftShift, KeyLeftAlt, KeyLeftMeta, KeySpace, KeyLeft, KeyRight, KeyHome, KeyEnd, KeyInsert} {
		if !allowed[code] {
			t.Errorf("key code %d must be enabled", code)
		}
	}
}
