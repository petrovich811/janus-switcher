package inject

import (
	"fmt"
	"strings"

	"github.com/petrovich811/janus-switcher/internal/convert"
)

// Sender is the narrow slice of a keyboard that layout switching needs.
type Sender interface {
	Chord(mods []uint16, key uint16) error
}

// ModTapper is implemented by keyboards that can press modifier keys alone,
// which is how the legacy Windows layout switch (Ctrl+Shift) is invoked.
type ModTapper interface {
	TapModifiers(mods []uint16) error
}

// shortKeys maps names of non-letter keys onto evdev codes.
var shortKeys = map[string]uint16{
	"insert": 110, "delete": 111, "home": 102, "end": 107,
	"pageup": 104, "pagedown": 109, "prior": 104, "next": 109,
	"enter": 28, "return": 28, "tab": 15, "space": 57,
	"backspace": 14, "escape": 1, "esc": 1,
	"up": 103, "down": 108, "left": 105, "right": 106,
	"pause": 119, "print": 99,
}

// ParseShortcut turns "ctrl+shift+c" into modifiers and a key code. Letters are
// resolved through the US layout, which is what physical key codes mean.
func ParseShortcut(s string) (mods []uint16, code uint16, err error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return nil, 0, fmt.Errorf("empty shortcut")
	}
	parts := strings.Split(s, "+")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case "ctrl", "control":
			mods = append(mods, KeyLeftCtrl)
			continue
		case "shift":
			mods = append(mods, KeyLeftShift)
			continue
		case "alt":
			mods = append(mods, KeyLeftAlt)
			continue
		case "super", "meta", "win":
			mods = append(mods, KeyLeftMeta)
			continue
		}
		if c, ok := shortKeys[p]; ok {
			code = c
			continue
		}
		if len(p) == 1 {
			if k, ok := convert.KeyFor(convert.LayoutUS, rune(p[0])); ok {
				code = k.Code
				if k.Shift {
					mods = append(mods, KeyLeftShift)
				}
				continue
			}
		}
		return nil, 0, fmt.Errorf("unknown key %q", p)
	}
	if code == 0 {
		return nil, 0, fmt.Errorf("no key in %q", s)
	}
	return mods, code, nil
}
