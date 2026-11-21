//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/petrovich811/janus-switcher/internal/clipboard"
	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/convert"
	"github.com/petrovich811/janus-switcher/internal/engine"
	"github.com/petrovich811/janus-switcher/internal/hotkey"
	"github.com/petrovich811/janus-switcher/internal/inject"
	"github.com/petrovich811/janus-switcher/internal/layout"
	"github.com/petrovich811/janus-switcher/internal/tray"
)

func runtimeName() string { return "linux" }

// initConsole is a no-op: a Linux binary already has stdout.
func initConsole() {}

func sessionName() string {
	return fmt.Sprintf("%s / %s", os.Getenv("XDG_SESSION_TYPE"), os.Getenv("XDG_CURRENT_DESKTOP"))
}

// lazyKeyboard creates the virtual device on first use: creating it at startup
// would make the desktop see a keyboard appear for no reason.
type lazyKeyboard struct {
	mu  sync.Mutex
	kb  *inject.Keyboard
	err error
}

func (l *lazyKeyboard) get() (*inject.Keyboard, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kb != nil {
		return l.kb, nil
	}
	if l.err != nil {
		return nil, l.err
	}
	kb, err := inject.NewKeyboard("Janus virtual keyboard")
	if err != nil {
		l.err = err
		return nil, err
	}
	l.kb = kb
	return kb, nil
}

func (l *lazyKeyboard) Chord(mods []uint16, key uint16) error {
	kb, err := l.get()
	if err != nil {
		return err
	}
	return kb.Chord(mods, key)
}

func (l *lazyKeyboard) Tap(key uint16) error {
	kb, err := l.get()
	if err != nil {
		return err
	}
	return kb.Tap(key)
}

func (l *lazyKeyboard) TypeText(text string, l2 convert.LayoutID) error {
	kb, err := l.get()
	if err != nil {
		return err
	}
	return kb.TypeText(text, l2)
}

func (l *lazyKeyboard) WaitModifiersReleased(timeout time.Duration) error {
	kb, err := l.get()
	if err != nil {
		return err
	}
	return kb.WaitModifiersReleased(timeout)
}

func (l *lazyKeyboard) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.kb != nil {
		err := l.kb.Close()
		l.kb = nil
		return err
	}
	return nil
}

// lazyClipboard keeps one X connection for the whole daemon lifetime.
type lazyClipboard struct {
	mu   sync.Mutex
	clip *clipboard.X11
}

func (l *lazyClipboard) get() (*clipboard.X11, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clip != nil {
		return l.clip, nil
	}
	c, err := clipboard.Open()
	if err != nil {
		return nil, err
	}
	l.clip = c
	return c, nil
}

func (l *lazyClipboard) ReadText() (string, error) {
	c, err := l.get()
	if err != nil {
		return "", err
	}
	return c.ReadText()
}

func (l *lazyClipboard) WriteText(text string) error {
	c, err := l.get()
	if err != nil {
		return err
	}
	return c.WriteText(text)
}

func (l *lazyClipboard) BeginCopy() string {
	c, err := l.get()
	if err != nil {
		return ""
	}
	return c.BeginCopy()
}

func (l *lazyClipboard) WaitCopied(token string, timeout time.Duration) (string, bool) {
	c, err := l.get()
	if err != nil {
		return "", false
	}
	return c.WaitCopied(token, timeout)
}

func (l *lazyClipboard) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clip != nil {
		l.clip.Close()
		l.clip = nil
	}
}

// linuxPlatform wires GNOME hotkeys, the D-Bus tray icon and X11 clipboard.
type linuxPlatform struct {
	kb   *lazyKeyboard
	clip *lazyClipboard
	lays layout.GNOME
	gh   *hotkey.GNOME
	bus  *dbus.Conn
	item *tray.Item
}

func newPlatform() platform {
	return &linuxPlatform{
		kb:   &lazyKeyboard{},
		clip: &lazyClipboard{},
		gh:   &hotkey.GNOME{Binary: exePath()},
	}
}

func (p *linuxPlatform) Keyboard() engine.Keyboard   { return p.kb }
func (p *linuxPlatform) Clipboard() engine.Clipboard { return p.clip }
func (p *linuxPlatform) Layouts() engine.Layouts     { return p.lays }
func (p *linuxPlatform) HotkeysManaged() bool        { return false }

func (p *linuxPlatform) ApplyHotkeys(a *app) error {
	cfg := a.store.Get()
	if cfg.Source != config.SourceGNOME {
		return p.gh.Remove("main", "alt")
	}
	if !hotkey.Available() {
		return fmt.Errorf("gsettings недоступен: это не сессия GNOME")
	}
	mainCombo, err := hotkey.Parse(cfg.HotkeyMain)
	if err != nil {
		return err
	}
	altCombo, err := hotkey.Parse(cfg.HotkeyAlt)
	if err != nil {
		return err
	}
	mainAccel, err := mainCombo.Accelerator()
	if err != nil {
		return err
	}
	altAccel, err := altCombo.Accelerator()
	if err != nil {
		return err
	}
	logf("apply hotkeys %s / %s", mainAccel, altAccel)
	return p.gh.Install(
		hotkey.Binding{Path: hotkey.Path("main"), Name: "Janus: переворот",
			Command: exePath() + " action main", Binding: mainAccel},
		hotkey.Binding{Path: hotkey.Path("alt"), Name: "Janus: слово",
			Command: exePath() + " action alt", Binding: altAccel},
	)
}

func (p *linuxPlatform) RemoveHotkeys(a *app) error {
	return p.gh.Remove("main", "alt")
}

func (p *linuxPlatform) Start(a *app) error {
	if conn, err := dbus.SessionBus(); err == nil {
		p.bus = conn
	} else {
		logf("session bus: %v", err)
	}
	if err := p.ApplyHotkeys(a); err != nil {
		logf("hotkeys: %v", err)
		fmt.Fprintln(os.Stderr, "горячие клавиши:", err)
	}
	if a.store.Get().ShowTray && p.bus != nil {
		item, err := tray.Add(p.bus, tray.Callbacks{
			OpenUI:  a.openUI,
			Toggle:  a.toggle,
			Quit:    a.shutdown,
			Enabled: func() bool { return a.store.Get().Enabled },
			Title: func() string {
				if a.store.Get().Enabled {
					return "Janus — включён"
				}
				return "Janus — выключен"
			},
		})
		if err != nil {
			logf("tray: %v", err)
			fmt.Fprintln(os.Stderr, "трей:", err)
		} else {
			p.item = item
			item.Watch(a.quit)
		}
	}
	return nil
}

func (p *linuxPlatform) Stop() {
	if p.item != nil {
		p.item.Close()
	}
	p.kb.Close()
	p.clip.Close()
	if p.bus != nil {
		p.bus.Close()
	}
}

func (p *linuxPlatform) RefreshIcon() {
	if p.item != nil {
		p.item.Refresh()
	}
}

// bus returns the session bus, connecting on first use so that a short-lived
// {`janus status`} process reports the tray state too.
func (p *linuxPlatform) busConn() *dbus.Conn {
	if p.bus == nil {
		if conn, err := dbus.SessionBus(); err == nil {
			p.bus = conn
		}
	}
	return p.bus
}

func (p *linuxPlatform) Status() map[string]any {
	st := map[string]any{}
	p.busConn()
	cfg := newAppStore().Get()

	if cfg.Source != config.SourceGNOME {
		st["hotkeys_ok"] = false
		st["hotkeys_detail"] = "источник клавиш: " + string(cfg.Source)
	} else if !hotkey.Available() {
		st["hotkeys_ok"] = false
		st["hotkeys_detail"] = "нет gsettings — не сессия GNOME"
	} else if b, err := p.gh.Current("main"); err != nil {
		st["hotkeys_ok"] = false
		st["hotkeys_detail"] = "сочетание не зарегистрировано"
	} else {
		pretty := b.Binding
		if c, err := hotkey.Parse(b.Binding); err == nil {
			pretty = c.String()
		}
		st["hotkeys_ok"] = true
		st["hotkeys_detail"] = pretty + " (GNOME)"
	}

	if err := inject.Available(); err != nil {
		st["uinput"] = false
		st["uinput_detail"] = err.Error()
	} else {
		st["uinput"] = true
		st["uinput_detail"] = "/dev/uinput доступен"
	}

	if _, err := p.clip.get(); err != nil {
		st["clipboard"] = false
		st["clipboard_detail"] = err.Error()
	} else {
		st["clipboard"] = true
		st["clipboard_detail"] = "X11-мост активен"
	}

	if err := p.lays.Available(); err != nil {
		st["layout_switch"] = false
		st["layouts_detail"] = "раскладки не читаются"
	} else {
		st["layout_switch"] = true
		st["layouts_detail"] = p.lays.Summary()
	}

	if p.item != nil && p.item.Registered() {
		st["tray"] = true
		st["tray_detail"] = "значок в панели"
	} else if p.item != nil {
		st["tray"] = false
		st["tray_detail"] = "расширение значков выгрузилось, значок вернётся после входа в сессию"
	} else if p.bus != nil && tray.Available(p.bus) && tray.RegisteredCount(p.bus) > 0 {
		st["tray"] = true
		st["tray_detail"] = "значок держит запущенный демон"
	} else if p.bus != nil && tray.Available(p.bus) {
		st["tray"] = false
		st["tray_detail"] = "значок не зарегистрирован"
	} else {
		st["tray"] = false
		st["tray_detail"] = "в GNOME значки скрыты — нужно расширение AppIndicator"
	}
	return st
}

func (p *linuxPlatform) Diag() []string {
	out := []string{}
	if n := keyDaemonCount(); n == 0 {
		out = append(out, "держатель клавиш:  gsd-media-keys НЕ запущен — сочетания не сработают, перезайдите в сессию")
	} else if n > 1 {
		out = append(out, fmt.Sprintf("держатель клавиш:  %d копии gsd-media-keys — лишние снимите, перезайдите в сессию", n))
	} else {
		out = append(out, "держатель клавиш:  gsd-media-keys работает")
	}
	out = append(out, fmt.Sprintf("сессия:            %s", sessionName()))
	out = append(out, fmt.Sprintf("gsettings:         %v", hotkey.Available()))
	if b, err := p.gh.Current("main"); err == nil {
		out = append(out, fmt.Sprintf("сочетание main:    %s -> %s", b.Binding, b.Command))
	} else {
		out = append(out, "сочетание main:    нет записи в GNOME")
	}
	if c, err := hotkey.Parse(newAppStore().Get().HotkeyMain); err == nil {
		out = append(out, fmt.Sprintf("сочетание в конфиге: %s (%s)", c.String(), c.Key))
	}
	if err := inject.Available(); err != nil {
		out = append(out, "uinput:            "+err.Error())
	} else {
		out = append(out, "uinput:            /dev/uinput открывается")
	}
	if _, err := p.clip.get(); err != nil {
		out = append(out, "буфер обмена:      "+err.Error())
	} else {
		out = append(out, "буфер обмена:      X11-мост отвечает")
	}
	if p.lays.Available() == nil {
		act, _ := p.lays.Active()
		out = append(out, fmt.Sprintf("раскладки:         %s (активна %s)", p.lays.Summary(), act))
	} else {
		out = append(out, "раскладки:         не читаются")
	}
	if conn := p.busConn(); conn != nil {
		out = append(out, fmt.Sprintf("трей:              watcher=%v элементов=%d",
			tray.Available(conn), tray.RegisteredCount(conn)))
	} else {
		out = append(out, "трей:              нет сессионного D-Bus")
	}
	return out
}

func (p *linuxPlatform) InstallAutostart() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	path := filepath.Join(base, "autostart", "janus.desktop")
	desktop := `[Desktop Entry]
Type=Application
Name=Janus
Comment=Двуликий: переворот текста между русской и английской раскладкой
Exec=` + exePath() + ` daemon
Icon=input-keyboard
Terminal=false
X-GNOME-Autostart-enabled=true
Categories=Utility;
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(desktop), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func (p *linuxPlatform) RemoveAutostart() error {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return os.Remove(filepath.Join(base, "autostart", "janus.desktop"))
}

func (p *linuxPlatform) OpenURL(url string) error {
	return exec.Command("xdg-open", url).Start()
}

func (p *linuxPlatform) EnableTray() error { return hotkey.EnableExtension() }

// keyDaemonCount counts gsd-media-keys: on GNOME it is the process that grabs
// the custom shortcuts, and without it every hotkey stays silent.
func keyDaemonCount() int {
	out, err := exec.Command("pgrep", "-c", "gsd-media-keys").Output()
	if err != nil {
		return 0
	}
	n := 0
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

// newAppStore loads the configuration without building a whole daemon.
func newAppStore() *config.Store {
	store, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}
	return store
}
