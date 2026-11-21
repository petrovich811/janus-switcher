//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/petrovich811/janus-switcher/internal/clipboard"
	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/engine"
	"github.com/petrovich811/janus-switcher/internal/hotkey"
	"github.com/petrovich811/janus-switcher/internal/inject"
	"github.com/petrovich811/janus-switcher/internal/layout"
	"github.com/petrovich811/janus-switcher/internal/winhost"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

var (
	shell32           = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")

	user32                           = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindowCall      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessIDCall = user32.NewProc("GetWindowThreadProcessId")
)

func runtimeName() string { return "windows" }

func initConsole() { winhost.EnsureConsole() }

func sessionName() string {
	v := os.Getenv("SESSIONNAME")
	if v == "" {
		v = "console"
	}
	return fmt.Sprintf("windows %s", v)
}

// windowsPlatform uses SendInput, the user32 clipboard, HKL layouts and the
// hidden window that holds the hotkeys and the tray icon.
type windowsPlatform struct {
	kb   *inject.Keyboard
	clip *clipboard.Win
	lays layout.Windows
	host *winhost.Host
	rel  func()

	mu      sync.Mutex
	started bool
}

func newPlatform() platform {
	clip, _ := clipboard.Open()
	kb, _ := inject.NewKeyboard("janus")
	return &windowsPlatform{kb: kb, clip: clip, lays: layout.Windows{}}
}

func (p *windowsPlatform) Keyboard() engine.Keyboard   { return p.kb }
func (p *windowsPlatform) Clipboard() engine.Clipboard { return p.clip }
func (p *windowsPlatform) Layouts() engine.Layouts     { return p.lays }
func (p *windowsPlatform) HotkeysManaged() bool        { return true }

func (p *windowsPlatform) bindings(a *app) ([]winhost.Binding, error) {
	cfg := a.store.Get()
	if cfg.Source == config.SourceOff {
		return nil, nil
	}
	mainB, err := winhost.ParseBinding("main", cfg.HotkeyMain)
	if err != nil {
		return nil, fmt.Errorf("основное сочетание: %w", err)
	}
	altB, err := winhost.ParseBinding("alt", cfg.HotkeyAlt)
	if err != nil {
		return nil, fmt.Errorf("второе сочетание: %w", err)
	}
	return []winhost.Binding{mainB, altB}, nil
}

// Start brings up the hidden window with hotkeys and the tray icon.
func (p *windowsPlatform) Start(a *app) error {
	if rel, err := winhost.CheckSingleInstance(); err != nil {
		return err
	} else {
		p.rel = rel
	}
	bindings, err := p.bindings(a)
	if err != nil {
		logf("bindings: %v", err)
	}
	host, err := winhost.Start(winhost.Callbacks{
		Action: func(slot string) {
			// Releasing Alt after a hotkey opens the window menu, and the next
			// Ctrl+C would go to the menu instead of the document.
			if p.altHotkey(slot) {
				_ = p.kb.DummyKey()
			}
			a.execute(slot)
		},
		OpenUI: a.openUI,
		Quit:   a.shutdown,
		Enabled: func() bool {
			return a.store.Get().Enabled
		},
		Toggle: func() bool { return a.toggle() },
		Title: func() string {
			if a.store.Get().Enabled {
				return "Janus — включён"
			}
			return "Janus — выключен"
		},
		Logf: logf,
	}, bindings)
	if err != nil {
		return err
	}
	p.host = host
	p.mu.Lock()
	p.started = true
	p.mu.Unlock()
	return nil
}

// altHotkey reports whether the hotkey of this slot uses Alt.
func (p *windowsPlatform) altHotkey(slot string) bool {
	accel := p.storeFor().HotkeyMain
	if slot == "alt" {
		accel = p.storeFor().HotkeyAlt
	}
	c, err := hotkey.Parse(accel)
	return err == nil && c.Alt
}

func (p *windowsPlatform) storeFor() config.Config { return newAppStore().Get() }

func (p *windowsPlatform) Stop() {
	if p.host != nil {
		p.host.Stop()
	}
	if p.rel != nil {
		p.rel()
	}
}

func (p *windowsPlatform) RefreshIcon() {}

// ApplyHotkeys re-registers the combinations after the settings changed.
func (p *windowsPlatform) ApplyHotkeys(a *app) error {
	if p.host == nil {
		return nil
	}
	bindings, err := p.bindings(a)
	if err != nil {
		return err
	}
	return p.host.ApplyBindings(bindings)
}

func (p *windowsPlatform) RemoveHotkeys(a *app) error {
	if p.host == nil {
		return nil
	}
	return p.host.ApplyBindings(nil)
}

func (p *windowsPlatform) Status() map[string]any {
	st := map[string]any{}
	cfg := newAppStore().Get()

	st["hotkeys_ok"] = false
	st["hotkeys_detail"] = "демон не запущен"
	if p.host != nil {
		errs := p.host.HotkeyErrors()
		if cfg.Source == config.SourceOff {
			st["hotkeys_ok"] = false
			st["hotkeys_detail"] = "источник клавиш: вручную"
		} else if msg, bad := errs["main"]; bad {
			st["hotkeys_detail"] = msg
		} else if b, err := winhost.ParseBinding("main", cfg.HotkeyMain); err == nil {
			st["hotkeys_ok"] = true
			st["hotkeys_detail"] = b.Text + " (Windows)"
		} else {
			st["hotkeys_detail"] = err.Error()
		}
	}

	if err := inject.Available(); err != nil {
		st["uinput"] = false
		st["uinput_detail"] = err.Error()
	} else {
		st["uinput"] = true
		st["uinput_detail"] = "SendInput доступен"
	}

	if _, err := p.clip.ReadText(); err != nil && !strings.Contains(err.Error(), "нет текста") {
		st["clipboard"] = false
		st["clipboard_detail"] = err.Error()
	} else {
		st["clipboard"] = true
		st["clipboard_detail"] = "буфер обмена доступен"
	}

	if err := p.lays.Available(); err != nil {
		st["layout_switch"] = false
		st["layouts_detail"] = err.Error()
	} else {
		st["layout_switch"] = true
		st["layouts_detail"] = p.lays.Summary()
	}

	if p.host != nil && p.host.TrayActive() {
		st["tray"] = true
		st["tray_detail"] = "значок в области уведомлений"
	} else {
		st["tray"] = false
		st["tray_detail"] = "значок не создан"
	}
	return st
}

func (p *windowsPlatform) Diag() []string {
	out := []string{}
	out = append(out, "система:            "+sessionName())
	out = append(out, fmt.Sprintf("размер INPUT:       %d байт (должно быть 40)", inputSize()))
	out = append(out, fmt.Sprintf("SendInput:          %v", inject.Available() == nil))

	cfg := newAppStore().Get()
	if b, err := winhost.ParseBinding("main", cfg.HotkeyMain); err == nil {
		out = append(out, fmt.Sprintf("сочетание main:     %s (mods=0x%04x vk=0x%02x)", b.Text, b.Mods, b.VK))
	} else {
		out = append(out, "сочетание main:     "+err.Error())
	}
	if b, err := winhost.ParseBinding("alt", cfg.HotkeyAlt); err == nil {
		out = append(out, fmt.Sprintf("сочетание alt:      %s (mods=0x%04x vk=0x%02x)", b.Text, b.Mods, b.VK))
	} else {
		out = append(out, "сочетание alt:      "+err.Error())
	}
	if p.host != nil {
		for slot, msg := range p.host.HotkeyErrors() {
			out = append(out, fmt.Sprintf("ошибка сочетания %s: %s", slot, msg))
		}
		out = append(out, fmt.Sprintf("значок в трее:      %v", p.host.TrayActive()))
	} else {
		out = append(out, "демон:              не запущен, сочетания не проверены")
	}

	probe := fmt.Sprintf("janus-diag-%d", time.Now().UnixNano()%100000)
	if err := p.clip.WriteText(probe); err != nil {
		out = append(out, "буфер записи:       "+err.Error())
	} else if got, err := p.clip.ReadText(); err != nil {
		out = append(out, "буфер чтения:       "+err.Error())
	} else if got != probe {
		out = append(out, fmt.Sprintf("буфер:              вернулось %q вместо %q", got, probe))
	} else {
		out = append(out, "буфер:              запись и чтение работают")
	}

	if p.lays.Available() == nil {
		act, _ := p.lays.Active()
		out = append(out, fmt.Sprintf("раскладки:          %s (активна %s)", p.lays.Summary(), act))
	} else {
		out = append(out, "раскладки:          не читаются")
	}

	out = append(out, "окно в фокусе:      "+foregroundInfo())
	return out
}

func inputSize() int { return 40 }

// foregroundInfo reports whether the focused window is elevated: typing into it
// is blocked by UIPI, and silence would look like a broken hotkey.
func foregroundInfo() string {
	hwnd, _, _ := procGetForegroundWindowCall.Call()
	if hwnd == 0 {
		return "нет"
	}
	var pid uint32
	procGetWindowThreadProcessIDCall.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "неизвестно"
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Sprintf("pid %d (нет доступа)", pid)
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Sprintf("pid %d", pid)
	}
	defer token.Close()
	var elevated uint32
	var size uint32 = 4
	if err := windows.GetTokenInformation(token, windows.TokenElevation,
		(*byte)(unsafe.Pointer(&elevated)), size, &size); err != nil {
		return fmt.Sprintf("pid %d", pid)
	}
	if elevated != 0 {
		return fmt.Sprintf("pid %d, окно с правами администратора: ввод в него заблокирован", pid)
	}
	return fmt.Sprintf("pid %d, обычные права", pid)
}

func (p *windowsPlatform) InstallAutostart() (string, error) {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	cmd := `"` + exePath() + `" daemon`
	if err := key.SetStringValue("JanusSwitcher", cmd); err != nil {
		return "", err
	}
	return `HKCU\` + runKeyPath, nil
}

func (p *windowsPlatform) RemoveAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.DeleteValue("JanusSwitcher")
}

func (p *windowsPlatform) OpenURL(url string) error {
	op, _ := windows.UTF16PtrFromString("open")
	u, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(u)), 0, 0, 1 /* SW_SHOWNORMAL */)
	if r <= 32 {
		return fmt.Errorf("ShellExecute: %v", callErr)
	}
	return nil
}

func (p *windowsPlatform) EnableTray() error { return nil }

// newAppStore loads the configuration without building a whole daemon.
func newAppStore() *config.Store {
	store, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}
	return store
}
