//go:build linux

// Package inject creates a virtual keyboard through /dev/uinput and types into
// the focused window. It works on X11 and on Wayland alike, because the events
// travel through the kernel input subsystem, exactly like a real keyboard.
package inject

import (
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/petrovich811/janus-switcher/internal/convert"
)

const (
	uiDevSetup   = 0x405c5503
	uiDevDestroy = 0x5502
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565

	evSyn = 0x00
	evKey = 0x01

	synReport  = 0
	keyRelease = 0
	keyPress   = 1
	keyRepeat  = 2
)

type inputID struct {
	BusType uint16
	Vendor  uint16
	Product uint16
	Version uint16
}

type uinputSetup struct {
	ID           inputID
	Name         [80]byte
	FFEffectsMax uint32
}

type inputEvent struct {
	Sec   int64
	Usec  int64
	Type  uint16
	Code  uint16
	Value int32
}

// whitespaceKey maps whitespace characters onto key codes.
func whitespaceKey(r rune) (uint16, bool) {
	switch r {
	case ' ':
		return KeySpace, true
	case '\t':
		return KeyTab, true
	case '\n':
		return KeyEnter, true
	}
	return 0, false
}

// Keyboard is a virtual keyboard backed by a /dev/uinput device.
type Keyboard struct {
	f    *os.File
	path string

	mu      sync.Mutex
	pressed map[uint16]bool
}

// allowedKeys is deliberately narrow. The device must be able to send the
// shortcuts Janus uses and nothing that can compose Ctrl+Alt+Del (Delete is not
// in the list) or trigger power/sleep/function keys.
var allowedKeys = []uint16{
	KeyEsc, KeyTab, KeyEnter, KeyLeftCtrl, KeyLeftShift, KeyLeftAlt, KeyLeftMeta,
	KeyRightCtrl, KeyRightShift, KeySpace, KeyCapsLock, KeyBackspace,
	KeyInsert, KeyHome, KeyEnd, KeyLeft, KeyRight, KeyUp, KeyDown,
	// digits 1..0
	2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
	// top row punctuation - = [ ] ; ' ` \ ,
	12, 13, 26, 27, 39, 40, 41, 43, 51, 52, 53,
	// letters QWERTY
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25,
	30, 31, 32, 33, 34, 35, 36, 37, 38,
	44, 45, 46, 47, 48, 49, 50,
}

var allowed = func() map[uint16]bool {
	m := make(map[uint16]bool, len(allowedKeys))
	for _, k := range allowedKeys {
		m[k] = true
	}
	return m
}()

// Available reports whether we may open /dev/uinput. It deliberately does not
// create a device: that would make the desktop see a keyboard appear and vanish
// on every settings-page refresh.
func Available() error {
	fd, err := unix.Open("/dev/uinput", unix.O_WRONLY|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return fmt.Errorf("/dev/uinput недоступен: %w", err)
	}
	return unix.Close(fd)
}

// WaitModifiersReleased gives the user time to let go of the hotkey. Linux does
// not expose the keyboard state to us, so this is a plain wait.
func (k *Keyboard) WaitModifiersReleased(timeout time.Duration) error {
	if timeout <= 0 {
		return nil
	}
	time.Sleep(timeout)
	return nil
}

// NewKeyboard opens /dev/uinput and registers a virtual keyboard named name.
func NewKeyboard(name string) (*Keyboard, error) {
	fd, err := unix.Open("/dev/uinput", unix.O_WRONLY|unix.O_NONBLOCK, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open /dev/uinput: %w", err)
	}
	f := os.NewFile(uintptr(fd), "/dev/uinput")
	k := &Keyboard{f: f, pressed: map[uint16]bool{}}
	cleanup := func(e error) (*Keyboard, error) {
		f.Close()
		return nil, e
	}
	// Enable the event types and every key code we may ever need.
	if err := k.ioctl(uiSetEvBit, uintptr(evKey)); err != nil {
		return cleanup(fmt.Errorf("UI_SET_EVBIT: %w", err))
	}
	for _, code := range allowedKeys {
		if err := k.ioctl(uiSetKeyBit, uintptr(code)); err != nil {
			return cleanup(fmt.Errorf("UI_SET_KEYBIT %d: %w", code, err))
		}
	}
	var setup uinputSetup
	setup.ID = inputID{BusType: 0x03 /* BUS_USB */, Vendor: 0x1d6b, Product: 0x0104, Version: 1}
	copy(setup.Name[:], name)
	if err := k.ioctl(uiDevSetup, uintptr(unsafe.Pointer(&setup))); err != nil {
		return cleanup(fmt.Errorf("UI_DEV_SETUP: %w", err))
	}
	if err := k.ioctl(0x5501 /* UI_DEV_CREATE */, 0); err != nil {
		return cleanup(fmt.Errorf("UI_DEV_CREATE: %w", err))
	}
	k.path = "/dev/input/janus-virtual-keyboard"
	time.Sleep(60 * time.Millisecond) // let the compositor notice the new device
	return k, nil
}

func (k *Keyboard) ioctl(req uintptr, arg uintptr) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, k.f.Fd(), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

// Close releases any held keys and destroys the virtual device.
func (k *Keyboard) Close() error {
	if k.f == nil {
		return nil
	}
	_ = k.ReleaseAll()
	_ = k.ioctl(uiDevDestroy, 0)
	return k.f.Close()
}

// ReleaseAll lifts every key this device still holds. Called on errors and on
// shutdown so a partial chord cannot leave Ctrl/Shift/Alt stuck.
func (k *Keyboard) ReleaseAll() error {
	k.mu.Lock()
	keys := make([]uint16, 0, len(k.pressed))
	for code := range k.pressed {
		keys = append(keys, code)
	}
	k.mu.Unlock()
	for _, code := range keys {
		if err := k.write(evKey, code, keyRelease); err != nil {
			return err
		}
		k.mu.Lock()
		delete(k.pressed, code)
		k.mu.Unlock()
	}
	return k.Sync()
}

func (k *Keyboard) write(typ, code uint16, value int32) error {
	ev := inputEvent{Type: typ, Code: code, Value: value}
	buf := (*[unsafe.Sizeof(inputEvent{})]byte)(unsafe.Pointer(&ev))[:]
	_, err := k.f.Write(buf)
	return err
}

// KeyDown/hold and release a single key code.
func (k *Keyboard) KeyDown(code uint16) error {
	if !allowed[code] {
		return fmt.Errorf("key code %d is not enabled on the Janus keyboard", code)
	}
	if err := k.write(evKey, code, keyPress); err != nil {
		return err
	}
	k.mu.Lock()
	k.pressed[code] = true
	k.mu.Unlock()
	return nil
}

// KeyUp releases a single key code.
func (k *Keyboard) KeyUp(code uint16) error {
	if !allowed[code] {
		return fmt.Errorf("key code %d is not enabled on the Janus keyboard", code)
	}
	if err := k.write(evKey, code, keyRelease); err != nil {
		return err
	}
	k.mu.Lock()
	delete(k.pressed, code)
	k.mu.Unlock()
	return nil
}

// Sync flushes pending events.
func (k *Keyboard) Sync() error { return k.write(evSyn, 0, synReport) }

// Tap presses and releases one key.
func (k *Keyboard) Tap(code uint16) (err error) {
	defer func() {
		if err != nil {
			_ = k.ReleaseAll()
		}
	}()
	if err := k.KeyDown(code); err != nil {
		return err
	}
	if err := k.Sync(); err != nil {
		return err
	}
	time.Sleep(8 * time.Millisecond)
	if err := k.KeyUp(code); err != nil {
		return err
	}
	return k.Sync()
}

// TapModifiers presses and releases modifier keys only.
func (k *Keyboard) TapModifiers(mods []uint16) error {
	for _, m := range mods {
		if err := k.KeyDown(m); err != nil {
			return err
		}
	}
	_ = k.Sync()
	time.Sleep(15 * time.Millisecond)
	for i := len(mods) - 1; i >= 0; i-- {
		if err := k.KeyUp(mods[i]); err != nil {
			return err
		}
	}
	return k.Sync()
}

// Chord presses mods in order, taps key, then releases the modifiers.
func (k *Keyboard) Chord(mods []uint16, key uint16) (err error) {
	defer func() {
		if err != nil {
			_ = k.ReleaseAll()
		}
	}()
	for _, m := range mods {
		if err := k.KeyDown(m); err != nil {
			return err
		}
	}
	if err := k.Sync(); err != nil {
		return err
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
	return k.Sync()
}

// TypeText types text assuming layout is the active system layout: for every
// rune it finds the physical key and presses Shift when needed.
func (k *Keyboard) TypeText(text string, layout convert.LayoutID) (err error) {
	defer func() {
		if err != nil {
			_ = k.ReleaseAll()
		}
	}()
	shift := false
	for _, r := range text {
		if code, ok := whitespaceKey(r); ok {
			if shift {
				_ = k.KeyUp(KeyLeftShift)
				shift = false
				_ = k.Sync()
			}
			if err := k.Tap(code); err != nil {
				return err
			}
			continue
		}
		key, ok := convert.KeyFor(layout, r)
		if !ok {
			continue
		}
		if key.Shift && !shift {
			if err := k.KeyDown(KeyLeftShift); err != nil {
				return err
			}
			shift = true
			_ = k.Sync()
		} else if !key.Shift && shift {
			if err := k.KeyUp(KeyLeftShift); err != nil {
				return err
			}
			shift = false
			_ = k.Sync()
		}
		if err := k.Tap(key.Code); err != nil {
			return err
		}
	}
	if shift {
		return k.KeyUp(KeyLeftShift)
	}
	return nil
}
