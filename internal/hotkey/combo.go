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
	// a named key (pause, insert, home, ...), punctuation on a US keyboard
	// (minus, equal, backslash, ...) or a keypad key (numpad0..9, numpadadd).
	Key string
}

// gtkNames maps our key names to the names GTK accelerators use.
var gtkNames = map[string]string{
	"pause": "Pause", "print": "Print", "insert": "Insert", "delete": "Delete",
	"home": "Home", "end": "End", "pageup": "Page_Up", "pagedown": "Page_Down",
	"space": "space", "return": "Return", "tab": "Tab", "backspace": "BackSpace",
	"escape": "Escape", "left": "Left", "right": "Right", "up": "Up", "down": "Down",
	"minus": "minus", "equal": "equal", "bracketleft": "bracketleft",
	"bracketright": "bracketright", "backslash": "backslash", "semicolon": "semicolon",
	"apostrophe": "apostrophe", "comma": "comma", "period": "period",
	"slash": "slash", "grave": "grave", "capslock": "Caps_Lock",
	"scrolllock": "Scroll_Lock", "menu": "Menu",
	"numpad0": "KP_0", "numpad1": "KP_1", "numpad2": "KP_2", "numpad3": "KP_3",
	"numpad4": "KP_4", "numpad5": "KP_5", "numpad6": "KP_6", "numpad7": "KP_7",
	"numpad8": "KP_8", "numpad9": "KP_9", "numpadadd": "KP_Add",
	"numpadsubtract": "KP_Subtract", "numpadmultiply": "KP_Multiply",
	"numpaddivide": "KP_Divide", "numpaddecimal": "KP_Decimal",
	"numpadseparator": "KP_Separator", "numpadenter": "KP_Enter",
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
	"capslock": "capslock", "menu": "menu", "apps": "menu",

	// Punctuation: names and the character on a US keyboard.
	"minus": "minus", "-": "minus",
	"equal": "equal", "=": "equal",
	"bracketleft": "bracketleft", "[": "bracketleft",
	"bracketright": "bracketright", "]": "bracketright",
	"backslash": "backslash", "\\": "backslash",
	"semicolon": "semicolon", ";": "semicolon",
	"apostrophe": "apostrophe", "'": "apostrophe",
	"comma": "comma", ",": "comma",
	"period": "period", ".": "period",
	"slash": "slash", "/": "slash",
	"grave": "grave", "`": "grave",

	// Keypad: browser (numpad0), GTK (KP_0) and common aliases.
	"numpad0": "numpad0", "kp_0": "numpad0",
	"numpad1": "numpad1", "kp_1": "numpad1",
	"numpad2": "numpad2", "kp_2": "numpad2",
	"numpad3": "numpad3", "kp_3": "numpad3",
	"numpad4": "numpad4", "kp_4": "numpad4",
	"numpad5": "numpad5", "kp_5": "numpad5",
	"numpad6": "numpad6", "kp_6": "numpad6",
	"numpad7": "numpad7", "kp_7": "numpad7",
	"numpad8": "numpad8", "kp_8": "numpad8",
	"numpad9": "numpad9", "kp_9": "numpad9",
	"numpadadd": "numpadadd", "add": "numpadadd", "kp_add": "numpadadd",
	"numpadsubtract": "numpadsubtract", "subtract": "numpadsubtract",
	"kp_subtract":    "numpadsubtract",
	"numpadmultiply": "numpadmultiply", "multiply": "numpadmultiply",
	"kp_multiply":  "numpadmultiply",
	"numpaddivide": "numpaddivide", "divide": "numpaddivide",
	"kp_divide":     "numpaddivide",
	"numpaddecimal": "numpaddecimal", "decimal": "numpaddecimal",
	"kp_decimal":      "numpaddecimal",
	"numpadseparator": "numpadseparator", "separator": "numpadseparator",
	"kp_separator": "numpadseparator",
	"numpadenter":  "numpadenter", "kp_enter": "numpadenter",
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
	if s, ok := displayNames[c.Key]; ok {
		return s
	}
	return strings.ToUpper(c.Key)
}

var displayNames = map[string]string{
	"pause": "Pause/Break", "print": "PrintScreen", "pageup": "PageUp",
	"pagedown": "PageDown", "capslock": "CapsLock", "scrolllock": "ScrollLock",
	"menu": "Menu", "minus": "-", "equal": "=", "bracketleft": "[",
	"bracketright": "]", "backslash": "\\", "semicolon": ";",
	"apostrophe": "'", "comma": ",", "period": ".", "slash": "/",
	"grave": "`", "numpad0": "Num 0", "numpad1": "Num 1", "numpad2": "Num 2",
	"numpad3": "Num 3", "numpad4": "Num 4", "numpad5": "Num 5", "numpad6": "Num 6",
	"numpad7": "Num 7", "numpad8": "Num 8", "numpad9": "Num 9",
	"numpadadd": "Num +", "numpadsubtract": "Num -", "numpadmultiply": "Num *",
	"numpaddivide": "Num /", "numpaddecimal": "Num .", "numpadseparator": "Num ,",
	"numpadenter": "Num Enter",
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

// IsBindable reports whether the combination may be registered globally.
// GNOME takes function keys on their own; everything else needs a modifier.
func (c Combo) IsBindable() bool { return c.HasModifier() || c.IsFunctionKey() }
