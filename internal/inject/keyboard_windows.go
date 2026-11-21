//go:build windows && (amd64 || arm64)

// Package inject types into the focused window. On Windows this is SendInput:
// text goes as KEYEVENTF_UNICODE (so the layout does not matter), shortcuts go
// as virtual-key codes.
package inject

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/petrovich811/janus-switcher/internal/convert"
)

// evdev-style key codes are our internal "physical key" language: the layout
// tables in internal/convert are written in them. This file translates them to
// Windows virtual-key codes.
var vkByKey = map[uint16]uint16{
	1: 0x1B,                                     // Esc
	2: 0x31, 3: 0x32, 4: 0x33, 5: 0x34, 6: 0x35, // 1..5
	7: 0x36, 8: 0x37, 9: 0x38, 10: 0x39, 11: 0x30, // 6..0
	12: 0xBD,                                         // minus
	13: 0xBB,                                         // equal
	14: 0x08,                                         // backspace
	15: 0x09,                                         // tab
	16: 0x51, 17: 0x57, 18: 0x45, 19: 0x52, 20: 0x54, // q w e r t
	21: 0x59, 22: 0x55, 23: 0x49, 24: 0x4F, 25: 0x50, // y u i o p
	26: 0xDB, 27: 0xDD, 43: 0xDC, // [ ] backslash
	28: 0x0D,           // enter
	29: 0xA2, 97: 0xA3, // left/right ctrl
	30: 0x41, 31: 0x53, 32: 0x44, 33: 0x46, 34: 0x47, // a s d f g
	35: 0x48, 36: 0x4A, 37: 0x4B, 38: 0x4C, // h j k l
	39: 0xBA, 40: 0xDE, // ; '
	41: 0xC0,           // grave
	42: 0xA0, 54: 0xA1, // left/right shift
	44: 0x5A, 45: 0x58, 46: 0x43, 47: 0x56, 48: 0x42, // z x c v b
	49: 0x4E, 50: 0x4D, // n m
	51: 0xBC, 52: 0xBE, 53: 0xBF, // , . /
	56: 0xA4, 100: 0xA5, // left/right alt
	57:  0x20,            // space
	58:  0x14,            // capslock
	102: 0x24, 107: 0x23, // home end
	103: 0x26, 108: 0x28, 105: 0x25, 106: 0x27, // arrows
	104: 0x21, 109: 0x22, // page up / down
	110: 0x2D, 111: 0x2E, // insert delete
	119: 0x13, // pause
	125: 0x5B, // left win
}

const (
	keyeventfKeyup    = 0x0002
	keyeventfExtended = 0x0001
	keyeventfUnicode  = 0x0004

	inputKeyboard = 1

	modNoRepeat = 0x4000
)

// Windows INPUT layout, asserted at init: 40 bytes on 64-bit.
type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

type input struct {
	typ uint32
	_   uint32
	// The union must be 8-byte aligned: keybdInput contains a uintptr.
	union [4]uint64
}

// Compile-time proof that our layout matches the Win32 INPUT (40 bytes on
// 64-bit). A runtime check would be invisible in a windowsgui binary.
var _ [unsafe.Sizeof(input{}) - 40]byte
var _ [40 - unsafe.Sizeof(input{})]byte

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procVkKeyScanW       = user32.NewProc("VkKeyScanW")
)

// Keyboard sends keystrokes with SendInput. It holds no OS resources.
type Keyboard struct {
	mu sync.Mutex
}

// NewKeyboard creates a keyboard sender. The name is ignored on Windows.
func NewKeyboard(name string) (*Keyboard, error) {
	return &Keyboard{}, nil
}

// Available reports whether keystrokes can be sent at all.
func Available() error {
	if err := procSendInput.Find(); err != nil {
		return fmt.Errorf("SendInput недоступен: %w", err)
	}
	return nil
}

// Close is a no-op on Windows.
func (k *Keyboard) Close() error { return nil }

func (k *Keyboard) send(in *input) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	n, _, callErr := procSendInput.Call(1, uintptr(unsafe.Pointer(in)), unsafe.Sizeof(input{}))
	if n == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return fmt.Errorf("SendInput: %v", errno)
		}
		return fmt.Errorf("SendInput не отправил событие (возможно, окно с повышенными правами)")
	}
	return nil
}

func newKeyInput(vk uint16, up bool) *input {
	in := &input{typ: inputKeyboard}
	ki := (*keybdInput)(unsafe.Pointer(&in.union[0]))
	ki.vk = vk
	if up {
		ki.flags |= keyeventfKeyup
	}
	return in
}

func newUnicodeInput(unit uint16, up bool) *input {
	in := &input{typ: inputKeyboard}
	ki := (*keybdInput)(unsafe.Pointer(&in.union[0]))
	ki.scan = unit
	ki.flags = keyeventfUnicode
	if up {
		ki.flags |= keyeventfKeyup
	}
	return in
}

func (k *Keyboard) vk(code uint16) (uint16, bool) {
	v, ok := vkByKey[code]
	return v, ok
}

// KeyDown presses one key.
func (k *Keyboard) KeyDown(code uint16) error {
	v, ok := k.vk(code)
	if !ok {
		return fmt.Errorf("unknown key code %d", code)
	}
	return k.send(newKeyInput(v, false))
}

// KeyUp releases one key.
func (k *Keyboard) KeyUp(code uint16) error {
	v, ok := k.vk(code)
	if !ok {
		return fmt.Errorf("unknown key code %d", code)
	}
	return k.send(newKeyInput(v, true))
}

// Sync is a no-op: SendInput delivers each event immediately.
func (k *Keyboard) Sync() error { return nil }

// Tap presses and releases one key.
func (k *Keyboard) Tap(code uint16) error {
	if err := k.KeyDown(code); err != nil {
		return err
	}
	time.Sleep(8 * time.Millisecond)
	return k.KeyUp(code)
}

// TapModifiers presses and releases modifier keys only, which is how the
// legacy Windows layout switch (Ctrl+Shift) is triggered.
func (k *Keyboard) TapModifiers(mods []uint16) error {
	for _, m := range mods {
		if err := k.KeyDown(m); err != nil {
			return err
		}
	}
	time.Sleep(20 * time.Millisecond)
	for i := len(mods) - 1; i >= 0; i-- {
		if err := k.KeyUp(mods[i]); err != nil {
			return err
		}
	}
	return nil
}

// Chord presses modifiers, taps the key, releases the modifiers.
func (k *Keyboard) Chord(mods []uint16, key uint16) error {
	for _, m := range mods {
		if err := k.KeyDown(m); err != nil {
			return err
		}
	}
	time.Sleep(12 * time.Millisecond)
	if err := k.Tap(key); err != nil {
		return err
	}
	time.Sleep(12 * time.Millisecond)
	for i := len(mods) - 1; i >= 0; i-- {
		if err := k.KeyUp(mods[i]); err != nil {
			return err
		}
	}
	return nil
}

// TypeText types text as Unicode, so the active layout does not matter.
// Control characters are sent as keys: applications do not turn a Unicode
// newline into an Enter press.
func (k *Keyboard) TypeText(text string, layout convert.LayoutID) error {
	prevCR := false
	for _, r := range text {
		if r == '\r' {
			if err := k.Tap(KeyEnter); err != nil {
				return err
			}
			prevCR = true
			continue
		}
		if r == '\n' {
			if prevCR {
				prevCR = false
				continue
			}
			if err := k.Tap(KeyEnter); err != nil {
				return err
			}
			continue
		}
		prevCR = false
		switch r {
		case '\t':
			if err := k.Tap(KeyTab); err != nil {
				return err
			}
			continue
		}
		units := utf16Units(r)
		for _, u := range units {
			if err := k.send(newUnicodeInput(u, false)); err != nil {
				return err
			}
			if err := k.send(newUnicodeInput(u, true)); err != nil {
				return err
			}
			time.Sleep(4 * time.Millisecond)
		}
	}
	return nil
}

func utf16Units(r rune) []uint16 {
	if r < 0x10000 {
		return []uint16{uint16(r)}
	}
	r -= 0x10000
	return []uint16{
		uint16(0xD800 + (r >> 10)),
		uint16(0xDC00 + (r & 0x3FF)),
	}
}

// DummyKey sends VK_PACKET (0xFF), a no-op key. Releasing Alt after a hotkey
// activates the window menu, and the following Ctrl+C would go to the menu
// instead of the document; the dummy key swallows that.
func (k *Keyboard) DummyKey() error {
	if err := k.send(newKeyInput(0xFF, false)); err != nil {
		return err
	}
	time.Sleep(5 * time.Millisecond)
	return k.send(newKeyInput(0xFF, true))
}

// WaitModifiersReleased waits until the user lets go of Ctrl, Shift, Alt and
// Win. Without this the hotkey modifiers would turn our Ctrl+C into Ctrl+Shift+C.
// If the keys are still held after the timeout, it reports an error instead of
// typing into a changed context.
func (k *Keyboard) WaitModifiersReleased(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	keys := []uint16{0x10, 0x11, 0x12, 0x5B, 0x5C} // shift, ctrl, alt, lwin, rwin
	for {
		pressed := false
		for _, vk := range keys {
			r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
			if int16(r&0x8000) != 0 {
				pressed = true
				break
			}
		}
		if !pressed {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("клавиши Ctrl/Shift/Alt всё ещё нажаты — переворот отменён")
		}
		time.Sleep(15 * time.Millisecond)
	}
}
