//go:build windows

// Package clipboard reads and writes the system clipboard. On Windows that is
// the plain user32 clipboard with CF_UNICODETEXT.
package clipboard

import (
	"fmt"
	"runtime"
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
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")

	clipboardWndProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
		r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
		return r
	})
)

// Win is the Windows clipboard.
type Win struct {
	mu       sync.Mutex
	last     string
	owner    uintptr // hidden message-only window used as the clipboard owner
	ownerErr error

	ownerOnce  sync.Once
	ownerReady chan struct{}
}

// msgStruct mirrors MSG. The window thread needs a message loop so that the
// owner window lives as long as the process, not as long as some recycled Go
// thread.
type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// ptr reinterprets an address returned by GlobalLock. The block lives outside
// the Go heap and stays valid until GlobalUnlock, so the conversion is safe;
// go vet cannot see that and reports the direct uintptr -> unsafe.Pointer form.
func ptr(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

var (
	classOnce sync.Once
	classErr  error
)

// Open creates the clipboard handle.
func Open() (*Win, error) { return &Win{}, nil }

// ownerWindow returns a hidden message-only window. OpenClipboard needs a real
// window handle: with NULL, EmptyClipboard clears the owner and the following
// SetClipboardData fails, which would break every conversion.
//
// The window is created on a dedicated locked thread with a message loop: a
// window belongs to its thread, and a thread that exits would take it away and
// break the clipboard for the rest of the process.
func (w *Win) ownerWindow() (uintptr, error) {
	w.ownerOnce.Do(func() {
		w.ownerReady = make(chan struct{})
		go w.ownerLoop()
	})
	<-w.ownerReady
	return w.owner, w.ownerErr
}

func (w *Win) ownerLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	const hwndMessage = ^uintptr(0) - 2 // (HWND)-3, HWND_MESSAGE
	className := "JanusClipboardOwner"
	name, err := windows.UTF16PtrFromString(className)
	if err != nil {
		w.ownerErr = err
		close(w.ownerReady)
		return
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
	classOnce.Do(func() {
		wc := wndClassEx{lpfnWndProc: clipboardWndProc, hInstance: inst, lpszClassName: name}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		if r, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("RegisterClassEx(%s): %v", className, callErr)
		}
	})
	if classErr != nil {
		w.ownerErr = classErr
		close(w.ownerReady)
		return
	}

	hwnd, _, callErr := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, inst, 0)
	if hwnd == 0 {
		w.ownerErr = fmt.Errorf("CreateWindowEx(%s): %v", className, callErr)
		close(w.ownerReady)
		return
	}
	w.owner = hwnd
	close(w.ownerReady)

	var m msgStruct
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (w *Win) open() error {
	hwnd, err := w.ownerWindow()
	if err != nil {
		return err
	}
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
	// OpenClipboard/CloseClipboard belong to one OS thread; without the lock
	// the goroutine could close another thread's clipboard.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
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
	// so the conversion is safe.
	units := unsafe.Slice((*uint16)(ptr(p)), size/2)
	s := windows.UTF16ToString(units)
	w.last = s
	return s, nil
}

// WriteText replaces the clipboard text. The data is handed to the system, so
// the memory must not be freed after a successful SetClipboardData.
func (w *Win) WriteText(text string) error {
	// See ReadText: the clipboard calls must stay on one OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w.mu.Lock()
	defer w.mu.Unlock()

	// Allocate and fill the block before emptying the clipboard, so a failed
	// allocation cannot leave the user with an empty clipboard.
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
	dst := unsafe.Slice((*uint16)(ptr(p)), len(units)+1)
	copy(dst, units)
	dst[len(units)] = 0
	procGlobalUnlock.Call(h)

	if err := w.open(); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer w.close()
	if r, _, callErr := procEmptyClipboard.Call(); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("не удалось очистить буфер: %v", callErr)
	}
	if r, _, callErr := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("не удалось записать буфер: %v", callErr)
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
