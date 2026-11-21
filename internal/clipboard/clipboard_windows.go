//go:build windows

// Package clipboard reads and writes the system clipboard. On Windows that is
// the plain user32 clipboard with CF_UNICODETEXT.
package clipboard

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	// A clipboard held by another process fails to open; retry briefly.
	openRetries = 25
	openSleep   = 8 * time.Millisecond
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procClipboardSeq     = user32.NewProc("GetClipboardSequenceNumber")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalSize       = kernel32.NewProc("GlobalSize")

	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	clipboardWndProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return r
	})
)

// Win is the Windows clipboard.
type Win struct {
	mu     sync.Mutex
	last   string
	owner  uintptr // hidden message-only window used as the clipboard owner
	ownsMu sync.Mutex
}

// Open creates the clipboard handle.
func Open() (*Win, error) { return &Win{}, nil }

// ownerWindow returns a hidden message-only window. OpenClipboard needs a real
// window handle: with NULL, EmptyClipboard clears the owner and the following
// SetClipboardData fails, which would break every conversion.
func (w *Win) ownerWindow() uintptr {
	w.ownsMu.Lock()
	defer w.ownsMu.Unlock()
	if w.owner != 0 {
		return w.owner
	}
	const hwndMessage = ^uintptr(0) - 2 // (HWND)-3, HWND_MESSAGE
	className := "JanusClipboardOwner"
	name, err := windows.UTF16PtrFromString(className)
	if err != nil {
		return 0
	}
	inst, _, _ := procGetModuleHandleW.Call(0)

	type wndClassEx struct {
		cbSize        uint32
		style         uint32
		lpfnWndProc   uintptr
		cbClsExtra    int32
		cbWndExtra    int32
		hInstance     uintptr
		hIcon         uintptr
		hCursor       uintptr
		hbrBackground uintptr
		lpszMenuName  *uint16
		lpszClassName *uint16
		hIconSm       uintptr
	}
	wc := wndClassEx{lpfnWndProc: clipboardWndProc, hInstance: inst, lpszClassName: name}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))) // may already exist

	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, inst, 0)
	w.owner = hwnd
	return hwnd
}

func (w *Win) open() error {
	hwnd := w.ownerWindow()
	for i := 0; i < openRetries; i++ {
		if r, _, _ := procOpenClipboard.Call(hwnd); r != 0 {
			return nil
		}
		time.Sleep(openSleep)
	}
	return fmt.Errorf("буфер обмена занят другой программой")
}

func (w *Win) close() { procCloseClipboard.Call() }

// Sequence returns the clipboard change counter.
func (w *Win) Sequence() uint32 {
	r, _, _ := procClipboardSeq.Call()
	return uint32(r)
}

// ReadText returns the clipboard text.
func (w *Win) ReadText() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.open(); err != nil {
		return "", err
	}
	defer w.close()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("в буфере нет текста")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("не удалось прочитать буфер")
	}
	defer procGlobalUnlock.Call(h)
	size, _, _ := procGlobalSize.Call(h)
	if size < 2 {
		return "", fmt.Errorf("в буфере пустая строка")
	}
	// GlobalLock returns a uintptr; the block stays locked until GlobalUnlock,
	// so the conversion is safe. go vet still reports it as unsafeptr.
	units := unsafe.Slice((*uint16)(unsafe.Pointer(p)), size/2)
	s := windows.UTF16ToString(units)
	w.last = s
	return s, nil
}

// WriteText replaces the clipboard text. The data is handed to the system, so
// the memory must not be freed after a successful SetClipboardData.
func (w *Win) WriteText(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.open(); err != nil {
		return err
	}
	defer w.close()

	procEmptyClipboard.Call()
	units := utf16.Encode([]rune(text))
	size := (len(units) + 1) * 2
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(size))
	if h == 0 {
		return fmt.Errorf("не удалось выделить память под буфер")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("не удалось заблокировать буфер")
	}
	// One slice covers the text and its terminator: GlobalAlloc does not
	// guarantee zeroed memory.
	// Same as above: the pointer is valid until GlobalUnlock.
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(units)+1)
	copy(dst, units)
	dst[len(units)] = 0
	procGlobalUnlock.Call(h)

	if r, _, err := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("не удалось записать буфер: %v", err)
	}
	w.last = text
	return nil
}

// BeginCopy records the clipboard sequence number. Nothing is written, so the
// user's clipboard and the Win+V history stay untouched.
func (w *Win) BeginCopy() string {
	return fmt.Sprintf("%d", w.Sequence())
}

// WaitCopied returns the text once the clipboard has been written by somebody
// else after BeginCopy.
func (w *Win) WaitCopied(token string, timeout time.Duration) (string, bool) {
	var start uint32
	_, _ = fmt.Sscanf(token, "%d", &start)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if seq := w.Sequence(); seq != start {
			if t, err := w.ReadText(); err == nil && t != "" {
				return t, true
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", false
}

// Close releases nothing: the clipboard belongs to the system.
func (w *Win) Close() {}
