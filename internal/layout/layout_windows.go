//go:build windows

// Package layout reports and switches the keyboard layout. On Windows layouts
// belong to threads, so we ask the thread of the focused window and ask that
// window to switch with WM_INPUTLANGCHANGEREQUEST.
package layout

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/petrovich811/janus-switcher/internal/inject"
)

const (
	wmInputLangChangeRequest = 0x0050
	langRussian              = 0x19
	langEnglish              = 0x09
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procGetKeyboardLayout        = user32.NewProc("GetKeyboardLayout")
	procGetKeyboardLayoutList    = user32.NewProc("GetKeyboardLayoutList")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
)

type guiThreadInfo struct {
	cbSize        uint32
	flags         uint32
	hwndActive    uintptr
	hwndFocus     uintptr
	hwndCapture   uintptr
	hwndMenuOwner uintptr
	hwndMoveSize  uintptr
	hwndCaret     uintptr
	rcCaret       [4]int32
}

// Windows switches the layout of the focused window.
type Windows struct{}

// Active reports the layout currently used by the focused window.
func Active() (string, error) {
	hkl, err := foregroundHKL()
	if err != nil {
		return "", err
	}
	return layoutName(hkl), nil
}

func (Windows) Active() (string, error) { return Active() }

func foregroundHKL() (uintptr, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0, fmt.Errorf("нет окна в фокусе")
	}
	var pid uint32
	tid, _, _ := procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if tid == 0 {
		return 0, fmt.Errorf("не удалось определить поток окна")
	}
	hkl, _, _ := procGetKeyboardLayout.Call(tid)
	if hkl == 0 {
		return 0, fmt.Errorf("не удалось прочитать раскладку")
	}
	return hkl, nil
}

func layoutName(hkl uintptr) string {
	lang := uint16(hkl) & 0x3FF
	switch lang {
	case langRussian:
		return "ru"
	case langEnglish:
		return "us"
	}
	return fmt.Sprintf("lang-%03x", lang)
}

// Layouts returns the installed layouts in Windows order.
func Layouts() []string {
	n, _, _ := procGetKeyboardLayoutList.Call(0, 0)
	if n == 0 {
		return nil
	}
	list := make([]uintptr, n)
	procGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&list[0])))
	out := make([]string, 0, n)
	for _, h := range list {
		out = append(out, layoutName(h))
	}
	return out
}

func hklFor(want string) uintptr {
	n, _, _ := procGetKeyboardLayoutList.Call(0, 0)
	if n == 0 {
		return 0
	}
	list := make([]uintptr, n)
	procGetKeyboardLayoutList.Call(n, uintptr(unsafe.Pointer(&list[0])))
	for _, h := range list {
		if strings.EqualFold(layoutName(h), want) {
			return h
		}
	}
	return 0
}

// focusedWindow returns the window that owns the caret, if known.
func focusedWindow() uintptr {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	tid, _, _ := procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if tid == 0 {
		return hwnd
	}
	info := guiThreadInfo{cbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&info))); r != 0 && info.hwndFocus != 0 {
		return info.hwndFocus
	}
	return hwnd
}

// SwitchTo asks the focused window to use the wanted layout and checks that it
// actually happened. Console windows ignore the message, so the system's own
// switching shortcuts are tried as a fallback.
func SwitchTo(kb inject.Sender, want string) error {
	cur, err := Active()
	if err == nil && strings.EqualFold(cur, want) {
		return nil
	}
	target := hklFor(want)
	if target == 0 {
		return fmt.Errorf("раскладка %q не установлена в системе", want)
	}
	hwnd := focusedWindow()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmInputLangChangeRequest, 0, target)
		if waitFor(want) {
			return nil
		}
	}
	// Fallbacks in the order Windows offers them: Win+Space (default since
	// Windows 10), then the legacy Ctrl+Shift and Alt+Shift taps. The list is
	// cycled, so a couple of attempts are needed.
	if kb != nil {
		for i := 0; i < 3; i++ {
			_ = kb.Chord([]uint16{inject.KeyLeftMeta}, inject.KeySpace)
			if waitFor(want) {
				return nil
			}
			if tapper, ok := kb.(inject.ModTapper); ok {
				_ = tapper.TapModifiers([]uint16{inject.KeyLeftCtrl, inject.KeyLeftShift})
				if waitFor(want) {
					return nil
				}
				_ = tapper.TapModifiers([]uint16{inject.KeyLeftAlt, inject.KeyLeftShift})
				if waitFor(want) {
					return nil
				}
			}
		}
	}
	now, _ := Active()
	return fmt.Errorf("не удалось переключить раскладку с %q на %q", now, want)
}

func (Windows) SwitchTo(kb inject.Sender, want string) error { return SwitchTo(kb, want) }

func waitFor(want string) bool {
	for i := 0; i < 10; i++ {
		time.Sleep(60 * time.Millisecond)
		if cur, err := Active(); err == nil && strings.EqualFold(cur, want) {
			return true
		}
	}
	return false
}

// Summary renders the installed layouts.
func Summary() string { return strings.Join(Layouts(), ", ") }

// Available reports whether layouts can be read.
func Available() error {
	if n, _, _ := procGetKeyboardLayoutList.Call(0, 0); n == 0 {
		return fmt.Errorf("список раскладок пуст")
	}
	return nil
}

func (Windows) Summary() string { return Summary() }

func (Windows) Available() error { return Available() }

// LayoutListOf renders layouts for diagnostics.
func LayoutListOf() string { return Summary() }
