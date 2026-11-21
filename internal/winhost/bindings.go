//go:build windows

package winhost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/petrovich811/janus-switcher/internal/hotkey"
)

// virtual-key codes for the named keys our settings can produce.
var vkByName = map[string]uint16{
	"pause": 0x13, "print": 0x2C, "insert": 0x2D, "delete": 0x2E,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
	"space": 0x20, "return": 0x0D, "tab": 0x09, "backspace": 0x08,
	"escape": 0x1B, "left": 0x25, "right": 0x27, "up": 0x26, "down": 0x28,
	"capslock": 0x14, "menu": 0x5D, "scrolllock": 0x91,
}

// ParseBinding turns a stored GTK accelerator into a RegisterHotKey binding.
func ParseBinding(slot, accelerator string) (Binding, error) {
	c, err := hotkey.Parse(accelerator)
	if err != nil {
		return Binding{}, err
	}
	var mods uint32
	if c.Ctrl {
		mods |= modControl
	}
	if c.Shift {
		mods |= modShift
	}
	if c.Alt {
		mods |= modAlt
	}
	if c.Super {
		mods |= modWin
	}
	vk, ok := vkForKey(c.Key)
	if !ok {
		return Binding{}, fmt.Errorf("клавиша %q не поддерживается в Windows", c.Key)
	}
	return Binding{Slot: slot, Mods: mods, VK: vk, Text: c.String()}, nil
}

func vkForKey(key string) (uint16, bool) {
	if v, ok := vkByName[key]; ok {
		return v, true
	}
	if len(key) == 1 {
		k := strings.ToUpper(key)
		if k >= "A" && k <= "Z" || k >= "0" && k <= "9" {
			return uint16(k[0]), true
		}
		return 0, false
	}
	if strings.HasPrefix(key, "f") {
		if n, err := strconv.Atoi(key[1:]); err == nil && n >= 1 && n <= 24 {
			return uint16(0x70 + n - 1), true
		}
	}
	return 0, false
}
