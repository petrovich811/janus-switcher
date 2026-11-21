//go:build windows

// Package winhost owns the one Win32 thread that is allowed to create windows:
// it holds a hidden window, registers the global hotkeys and draws the tray
// icon. Everything else in the daemon talks to it through posted jobs.
package winhost

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/tray"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmNull          = 0x0000
	wmHotkey        = 0x0312
	wmTray          = 0x8001 // WM_APP + 1
	wmJob           = 0x8002 // WM_APP + 2
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	menuOpenUI = 100
	menuToggle = 101
	menuQuit   = 102

	imageIcon      = 1
	lrLoadFromFile = 0x0010
	smCxSmIcon     = 49

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoNotify    = 0x0080

	errAlreadyExists = 183
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procRegisterWindowMsg   = user32.NewProc("RegisterWindowMessageW")
	procLoadImageW          = user32.NewProc("LoadImageW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procDestroyIcon         = user32.NewProc("DestroyIcon")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")

	wndProcCallback = syscall.NewCallback(wndProc)
	classOnce       sync.Once
	classErr        error
	taskbarCreated  uint32
)

// Binding is one global hotkey.
type Binding struct {
	Slot string // "main" or "alt"
	Mods uint32
	VK   uint16
	Text string // what to show in logs
}

// Callbacks are the daemon operations the window can trigger.
type Callbacks struct {
	Action  func(slot string)
	OpenUI  func()
	Quit    func()
	Enabled func() bool
	Toggle  func() bool
	Title   func() string
	Logf    func(format string, args ...any)
}

// Host is the hidden window plus tray icon.
type Host struct {
	cb Callbacks

	mu        sync.Mutex
	hwnd      uintptr
	ready     chan error
	jobs      []func()
	bindings  []Binding
	hotkeyErr map[string]string
	trayOn    bool
	icon      uintptr
	iconPath  string
	quitOnce  sync.Once
}

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

type msgStruct struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

type point struct{ x, y int32 }

type guid struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

// notifyIconData is the full NOTIFYICONDATAW (Vista and later, 976 bytes on
// 64-bit). A truncated version is accepted by no documented structure version,
// so the shell may ignore the icon entirely.
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         guid
	hBalloonIcon     uintptr
}

// Compile-time proof of the layout (976 bytes on 64-bit).
var _ [unsafe.Sizeof(notifyIconData{}) - 976]byte
var _ [976 - unsafe.Sizeof(notifyIconData{})]byte

var current *Host
var currentMu sync.Mutex

// CheckSingleInstance stops a second copy from fighting over the hotkeys.
func CheckSingleInstance() (func(), error) {
	// Local\ scopes the mutex to this session, so a second user (RDP, fast user
	// switching) can still run their own copy.
	name, err := windows.UTF16PtrFromString("Local\\janus-switcher")
	if err != nil {
		return nil, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("CreateMutex: %v", callErr)
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errAlreadyExists {
		windows.CloseHandle(windows.Handle(h))
		return nil, fmt.Errorf("демон уже запущен")
	}
	return func() { windows.CloseHandle(windows.Handle(h)) }, nil
}

func logf(cb Callbacks, format string, args ...any) {
	if cb.Logf != nil {
		cb.Logf(format, args...)
	}
}

// Start brings the window and the tray icon up and begins pumping messages.
func Start(cb Callbacks, bindings []Binding) (*Host, error) {
	h := &Host{cb: cb, ready: make(chan error, 1), bindings: bindings,
		hotkeyErr: map[string]string{}}
	currentMu.Lock()
	current = h
	currentMu.Unlock()
	go h.loop()
	if err := <-h.ready; err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Host) loop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := registerClass(); err != nil {
		h.ready <- err
		return
	}
	className, _ := windows.UTF16PtrFromString("JanusSwitcherWindow")
	title, _ := windows.UTF16PtrFromString("Janus")
	inst, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, callErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, inst, 0)
	if hwnd == 0 {
		h.ready <- fmt.Errorf("CreateWindowEx: %v", callErr)
		return
	}
	h.mu.Lock()
	h.hwnd = hwnd
	h.mu.Unlock()

	h.applyBindings(h.bindings)
	h.addTray()

	h.ready <- nil

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

func registerClass() error {
	classOnce.Do(func() {
		name, err := windows.UTF16PtrFromString("JanusSwitcherWindow")
		if err != nil {
			classErr = err
			return
		}
		inst, _, _ := procGetModuleHandleW.Call(0)
		wc := wndClassEx{
			lpfnWndProc:   wndProcCallback,
			hInstance:     inst,
			lpszClassName: name,
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		if r, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("RegisterClassEx: %v", callErr)
			return
		}
		created, _ := windows.UTF16PtrFromString("TaskbarCreated")
		r, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(created)))
		taskbarCreated = uint32(r)
	})
	return classErr
}

// post runs fn on the window thread.
func (h *Host) post(fn func()) {
	h.mu.Lock()
	h.jobs = append(h.jobs, fn)
	hwnd := h.hwnd
	h.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, wmJob, 0, 0)
	}
}

func (h *Host) drainJobs() {
	h.mu.Lock()
	jobs := h.jobs
	h.jobs = nil
	h.mu.Unlock()
	for _, fn := range jobs {
		fn()
	}
}

// ApplyBindings re-registers the hotkeys with new key combinations.
func (h *Host) ApplyBindings(b []Binding) error {
	h.post(func() { h.applyBindings(b) })
	return nil
}

func (h *Host) applyBindings(b []Binding) {
	h.mu.Lock()
	old := h.bindings
	h.bindings = b
	hwnd := h.hwnd
	h.mu.Unlock()

	for _, o := range old {
		procUnregisterHotKey.Call(hwnd, uintptr(hotkeyID(o.Slot)))
	}
	errs := map[string]string{}
	for _, nb := range b {
		id := hotkeyID(nb.Slot)
		r, _, callErr := procRegisterHotKey.Call(hwnd, uintptr(id), uintptr(nb.Mods|modNoRepeat), uintptr(nb.VK))
		if r == 0 {
			msg := "не зарегистрировано"
			if errno, ok := callErr.(syscall.Errno); ok && errno == 1409 {
				msg = "сочетание занято другой программой"
			} else if callErr != nil {
				msg = fmt.Sprintf("ошибка %v", callErr)
			}
			errs[nb.Slot] = msg
			logf(h.cb, "hotkey %s (%s): %s", nb.Slot, nb.Text, msg)
		} else {
			logf(h.cb, "hotkey %s registered: %s", nb.Slot, nb.Text)
		}
	}
	h.mu.Lock()
	h.hotkeyErr = errs
	h.mu.Unlock()
	if h.trayOn {
		h.updateTray()
	}
}

func hotkeyID(slot string) int {
	if slot == "alt" {
		return 2
	}
	return 1
}

// HotkeyErrors reports which bindings failed.
func (h *Host) HotkeyErrors() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.hotkeyErr))
	for k, v := range h.hotkeyErr {
		out[k] = v
	}
	return out
}

// TrayActive reports whether the icon sits in the notification area.
func (h *Host) TrayActive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.trayOn
}

// Stop closes the window, which ends the message loop.
func (h *Host) Stop() {
	h.quitOnce.Do(func() {
		h.mu.Lock()
		hwnd := h.hwnd
		h.mu.Unlock()
		h.post(func() {
			h.removeTray()
			if hwnd != 0 {
				procDestroyWindow.Call(hwnd)
			}
			procPostQuitMessage.Call(0)
		})
	})
}

// ---------------------------------------------------------------- tray icon

func (h *Host) addTray() {
	h.mu.Lock()
	hwnd := h.hwnd
	h.mu.Unlock()

	if h.icon == 0 {
		h.icon = h.loadIcon()
	}
	if taskbarCreated == 0 {
		created, _ := windows.UTF16PtrFromString("TaskbarCreated")
		r, _, _ := procRegisterWindowMsg.Call(uintptr(unsafe.Pointer(created)))
		taskbarCreated = uint32(r)
	}

	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTray
	nid.hIcon = h.icon
	copyTip(&nid.szTip, h.tip())

	r, _, callErr := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	ok := r != 0
	h.mu.Lock()
	h.trayOn = ok
	h.mu.Unlock()
	if !ok {
		logf(h.cb, "tray add failed: %v", callErr)
	} else {
		logf(h.cb, "tray icon added")
	}
}

func (h *Host) removeTray() {
	h.mu.Lock()
	hwnd := h.hwnd
	on := h.trayOn
	h.mu.Unlock()
	if !on {
		return
	}
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	h.mu.Lock()
	h.trayOn = false
	h.mu.Unlock()
}

func (h *Host) updateTray() {
	h.mu.Lock()
	hwnd := h.hwnd
	on := h.trayOn
	h.mu.Unlock()
	if !on {
		return
	}
	var nid notifyIconData
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifTip | nifIcon
	nid.hIcon = h.icon
	copyTip(&nid.szTip, h.tip())
	if r, _, _ := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		// Some shells want a fresh NIM_ADD after Explorer restarted.
		procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	}
}

func (h *Host) tip() string {
	if h.cb.Title != nil {
		return h.cb.Title()
	}
	return "Janus"
}

func copyTip(dst *[128]uint16, s string) {
	u, err := windows.UTF16FromString(truncateTip(s))
	if err != nil {
		return
	}
	copy(dst[:], u)
}

func truncateTip(s string) string {
	const max = 120 // leave room for the terminator in the 128 unit field
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func (h *Host) loadIcon() uintptr {
	dir := config.Dir()
	path := filepath.Join(dir, "janus.ico")
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if err := tray.WriteICO(path, 32); err != nil {
			logf(h.cb, "icon write: %v", err)
		}
	}
	h.mu.Lock()
	h.iconPath = path
	h.mu.Unlock()
	px, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	if px == 0 {
		px = 32
	}
	p, err := windows.UTF16PtrFromString(path)
	if err == nil {
		r, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(p)), imageIcon,
			px, px, lrLoadFromFile)
		if r != 0 {
			return r
		}
		logf(h.cb, "LoadImage(%s) failed, using the default icon", path)
	}
	// Always show something.
	r, _, _ := procLoadImageW.Call(0, uintptr(32512) /* IDI_APPLICATION */, imageIcon, 0, 0, 0)
	return r
}

func (h *Host) showMenu() {
	h.mu.Lock()
	hwnd := h.hwnd
	h.mu.Unlock()

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	open, _ := windows.UTF16PtrFromString("Открыть настройки")
	toggle := "Включить"
	if h.cb.Enabled != nil && h.cb.Enabled() {
		toggle = "Выключить"
	}
	togglePtr, _ := windows.UTF16PtrFromString(toggle)
	quitPtr, _ := windows.UTF16PtrFromString("Выход")

	procAppendMenuW.Call(menu, 0, menuOpenUI, uintptr(unsafe.Pointer(open)))
	procAppendMenuW.Call(menu, 0, menuToggle, uintptr(unsafe.Pointer(togglePtr)))
	procAppendMenuW.Call(menu, 0x00000800 /* MF_SEPARATOR */, 0, 0)
	procAppendMenuW.Call(menu, 0, menuQuit, uintptr(unsafe.Pointer(quitPtr)))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	r, _, _ := procTrackPopupMenu.Call(menu,
		tpmRightButton|tpmReturnCmd|tpmNoNotify,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	// Windows keeps the menu stuck without this.
	procPostMessageW.Call(hwnd, wmNull, 0, 0)

	switch int32(r) {
	case menuOpenUI:
		if h.cb.OpenUI != nil {
			h.cb.OpenUI()
		}
	case menuToggle:
		if h.cb.Toggle != nil {
			h.cb.Toggle()
		}
		h.updateTray()
	case menuQuit:
		if h.cb.Quit != nil {
			h.cb.Quit()
		}
	}
}

func wndProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	currentMu.Lock()
	h := current
	currentMu.Unlock()

	switch message {
	case wmHotkey:
		if h != nil && h.cb.Action != nil {
			slot := "main"
			if wparam == 2 {
				slot = "alt"
			}
			go h.cb.Action(slot)
		}
		return 0
	case wmJob:
		if h != nil {
			h.drainJobs()
		}
		return 0
	case wmTray:
		if h != nil {
			switch uint32(lparam) & 0xFFFF {
			case wmLButtonUp, wmLButtonDblClk:
				if h.cb.OpenUI != nil {
					go h.cb.OpenUI()
				}
			case wmRButtonUp:
				h.showMenu()
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	if h != nil && taskbarCreated != 0 && message == taskbarCreated {
		// Explorer restarted: the icon is gone, the hotkeys are not. Registering
		// them again would fail with ERROR_HOTKEY_ALREADY_REGISTERED.
		go func() { h.post(func() { h.addTray() }) }()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}
