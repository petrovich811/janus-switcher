// Package hotkey turns key combinations into GNOME accelerators and back.
package hotkey

import (
	"fmt"
	"strings"
)

// Combo is a parsed key combination.
type Combo struct {
	Ctrl  bool
	Shift bool
	Alt   bool
	Super bool
	// Key is a normalised key name: a lowercase letter, a digit, "f1".."f24",
	// or one of pause, print, insert, delete, home, end, pageup, pagedown,
	// space, return, tab, backspace, escape, left, right, up, down.
	Key string
}

// gtkNames maps our key names to the names GTK accelerators use.
var gtkNames = map[string]string{
	"pause": "Pause", "print": "Print", "insert": "Insert", "delete": "Delete",
	"home": "Home", "end": "End", "pageup": "Page_Up", "pagedown": "Page_Down",
	"space": "space", "return": "Return", "tab": "Tab", "backspace": "BackSpace",
	"escape": "Escape", "left": "Left", "right": "Right", "up": "Up", "down": "Down",
}

// Parse understands both "ctrl+shift+pause" and "<Control><Shift>Pause".
func Parse(s string) (Combo, error) {
	var c Combo
	s = strings.TrimSpace(s)
	if s == "" {
		return c, fmt.Errorf("empty combination")
	}
	parts := splitCombo(s)
	for i, p := range parts {
		tok := strings.ToLower(strings.Trim(p, "<> "))
		if tok == "" {
			continue
		}
		if isModifier(tok) {
			switch normMod(tok) {
			case "ctrl":
				c.Ctrl = true
			case "shift":
				c.Shift = true
			case "alt":
				c.Alt = true
			case "super":
				c.Super = true
			}
			continue
		}
		if i != len(parts)-1 {
			continue
		}
		k, err := normaliseKey(tok)
		if err != nil {
			return c, err
		}
		c.Key = k
	}
	if c.Key == "" {
		return c, fmt.Errorf("no key in %q", s)
	}
	return c, nil
}

func splitCombo(s string) []string {
	if strings.Contains(s, "><") || strings.HasPrefix(s, "<") {
		var out []string
		cur := strings.Builder{}
		for _, r := range s {
			switch r {
			case '<':
				if cur.Len() > 0 {
					out = append(out, cur.String())
					cur.Reset()
				}
			case '>':
				out = append(out, cur.String())
				cur.Reset()
			default:
				cur.WriteRune(r)
			}
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
		return out
	}
	return strings.Split(s, "+")
}

func isModifier(t string) bool {
	switch normMod(t) {
	case "ctrl", "shift", "alt", "super":
		return true
	}
	return false
}

func normMod(t string) string {
	switch strings.ToLower(t) {
	case "ctrl", "control", "primary":
		return "ctrl"
	case "shift":
		return "shift"
	case "alt", "mod1":
		return "alt"
	case "super", "meta", "win", "mod4":
		return "super"
	}
	return ""
}

// keyAliases maps browser and evdev spellings onto our canonical names.
var keyAliases = map[string]string{
	"pause": "pause", "break": "pause", "scrolllock": "scrolllock",
	"printscreen": "print", "print": "print", "sysrq": "print",
	"insert": "insert", "ins": "insert", "delete": "delete", "del": "delete",
	"home": "home", "end": "end",
	"pageup": "pageup", "pgup": "pageup", "prior": "pageup",
	"pagedown": "pagedown", "pgdn": "pagedown", "next": "pagedown",
	"space": "space", "spacebar": "space",
	"enter": "return", "return": "return",
	"tab": "tab", "backspace": "backspace", "escape": "escape", "esc": "escape",
	"arrowleft": "left", "left": "left", "arrowright": "right", "right": "right",
	"arrowup": "up", "up": "up", "arrowdown": "down", "down": "down",
	"capslock": "capslock", "menu": "menu",
}

func normaliseKey(tok string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(tok))
	if a, ok := keyAliases[t]; ok {
		return a, nil
	}
	if len(t) == 1 {
		r := rune(t[0])
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return t, nil
		}
	}
	if strings.HasPrefix(t, "f") && len(t) <= 3 {
		var n int
		if _, err := fmt.Sscanf(t[1:], "%d", &n); err == nil && n >= 1 && n <= 24 {
			return fmt.Sprintf("f%d", n), nil
		}
	}
	if strings.HasPrefix(t, "numpad") {
		return t, nil
	}
	return "", fmt.Errorf("unknown key %q", tok)
}

// IsEmpty reports whether the combination has no key.
func (c Combo) IsEmpty() bool { return c.Key == "" }

// String renders a human readable form such as "Ctrl+Shift+Pause".
func (c Combo) String() string {
	var parts []string
	if c.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if c.Shift {
		parts = append(parts, "Shift")
	}
	if c.Alt {
		parts = append(parts, "Alt")
	}
	if c.Super {
		parts = append(parts, "Super")
	}
	return strings.Join(append(parts, c.displayKey()), "+")
}

func (c Combo) displayKey() string {
	switch c.Key {
	case "pause":
		return "Pause/Break"
	case "print":
		return "PrintScreen"
	case "pageup":
		return "PageUp"
	case "pagedown":
		return "PageDown"
	}
	return strings.ToUpper(c.Key)
}

// Accelerator renders the GNOME/GTK accelerator string.
func (c Combo) Accelerator() (string, error) {
	if c.Key == "" {
		return "", fmt.Errorf("empty combination")
	}
	var b strings.Builder
	if c.Ctrl {
		b.WriteString("<Control>")
	}
	if c.Alt {
		b.WriteString("<Alt>")
	}
	if c.Shift {
		b.WriteString("<Shift>")
	}
	if c.Super {
		b.WriteString("<Super>")
	}
	if name, ok := gtkNames[c.Key]; ok {
		b.WriteString(name)
	} else if strings.HasPrefix(c.Key, "f") {
		b.WriteString(strings.ToUpper(c.Key))
	} else {
		b.WriteString(c.Key)
	}
	return b.String(), nil
}

// HasModifier reports whether any modifier is set.
func (c Combo) HasModifier() bool { return c.Ctrl || c.Alt || c.Super || c.Shift }

// IsFunctionKey reports whether the key needs no modifier to be grabbed.
func (c Combo) IsFunctionKey() bool {
	return strings.HasPrefix(c.Key, "f") || c.Key == "pause" || c.Key == "print"
}
