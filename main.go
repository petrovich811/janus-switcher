// Command janus is a small Punto Switcher replacement: it flips text between the Russian
// and English layouts with a hotkey, shows a tray icon and keeps its settings
// in a local web page. It runs on Linux (GNOME) and on Windows.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/engine"
	"github.com/petrovich811/janus-switcher/internal/ipc"
	"github.com/petrovich811/janus-switcher/internal/server"
)

// Version is the program version.
const Version = "1.1.1"

// platform is the per-OS half of the daemon.
type platform interface {
	// Keyboard, Clipboard and Layouts are the pieces the engine works with.
	Keyboard() engine.Keyboard
	Clipboard() engine.Clipboard
	Layouts() engine.Layouts
	// Start takes the global hotkeys and puts the icon in the tray.
	Start(a *app) error
	Stop()
	// Status and Diag describe what works right now.
	Status() map[string]any
	Diag() []string
	// InstallAutostart makes the daemon start with the session.
	InstallAutostart() (string, error)
	RemoveAutostart() error
	OpenURL(url string) error
	// EnableTray switches tray icons on where the desktop hides them.
	EnableTray() error
	// HotkeysManaged is true when the daemon owns the hotkeys itself.
	HotkeysManaged() bool
}

type app struct {
	store *config.Store
	eng   *engine.Engine
	plat  platform

	addr     string
	quit     chan struct{}
	quitOnce sync.Once

	statusMu   sync.Mutex
	statusAt   time.Time
	statusSnap map[string]any
}

func (a *app) shutdown() {
	a.quitOnce.Do(func() { close(a.quit) })
}

func (a *app) uiURL() string { return "http://" + a.addr + "/" }

func (a *app) toggle() bool {
	cfg, _ := a.store.Update(func(c *config.Config) { c.Enabled = !c.Enabled })
	a.invalidateStatus()
	a.refreshIcon()
	return cfg.Enabled
}

// refreshIcon updates the tray tooltip after a state change.
func (a *app) refreshIcon() {
	if r, ok := a.plat.(interface{ RefreshIcon() }); ok {
		r.RefreshIcon()
	}
}

func (a *app) openUI() {
	_ = a.plat.OpenURL(a.uiURL())
}

func (a *app) invalidateStatus() {
	a.statusMu.Lock()
	a.statusAt = time.Time{}
	a.statusMu.Unlock()
}

// status is cached for a moment: the settings page asks for it often, and
// gathering it means talking to the desktop.
func (a *app) status() map[string]any {
	a.statusMu.Lock()
	if time.Since(a.statusAt) < time.Second && a.statusSnap != nil {
		snap := a.statusSnap
		a.statusMu.Unlock()
		return snap
	}
	a.statusMu.Unlock()

	st := map[string]any{
		"config_path": a.store.FilePath(),
		"exe_path":    exePath(),
		"log_path":    logPath(),
		"session":     sessionName(),
	}
	for k, v := range a.plat.Status() {
		st[k] = v
	}

	a.statusMu.Lock()
	a.statusSnap = st
	a.statusAt = time.Now()
	a.statusMu.Unlock()
	return st
}

func main() {
	initConsole()
	cmd := "daemon"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "daemon":
		runDaemon()
	case "action":
		runAction()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "status":
		runStatus()
	case "diag":
		runDiag()
	case "convert":
		runConvert()
	case "ui":
		runUI()
	case "extension":
		runExtension()
	case "version", "-v", "--version":
		fmt.Println("janus " + Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Print(`janus ` + Version + ` — переворот текста между раскладками

  janus daemon            запустить демон со значком в трее и веб-настройками
  janus action main|alt   выполнить переворот (так зовут горячие клавиши)
  janus convert ТЕКСТ     перевести текст из терминала
  janus status            показать состояние
  janus diag              диагностика для отчёта о проблеме
  janus ui                открыть настройки в браузере
  janus install           прописать горячие клавиши и автозапуск
  janus uninstall         убрать горячие клавиши и автозапуск
  janus extension         включить расширение значков в трее (GNOME)
`)
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return "janus"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// ---------------------------------------------------------------- logging

var (
	logMu   sync.Mutex
	logFile *os.File
)

func logPath() string { return filepath.Join(config.Dir(), "janus.log") }

func initLog() {
	f, err := os.OpenFile(logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	if st, err := f.Stat(); err == nil && st.Size() > 512*1024 {
		_ = f.Truncate(0)
	}
	logFile = f
}

func logf(format string, args ...any) {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return
	}
	fmt.Fprintf(logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// ---------------------------------------------------------------- daemon

func newApp() *app {
	store, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
	}
	initLog()
	p := newPlatform()
	a := &app{store: store, plat: p, quit: make(chan struct{})}
	a.eng = engine.New(store, p.Keyboard(), p.Clipboard(), p.Layouts())
	a.eng.Logf = logf
	return a
}

func runDaemon() {
	a := newApp()
	cfg := a.store.Get()

	// Windows claims its single-instance mutex before the IPC port file is
	// touched, so a second copy cannot break the running daemon.
	if pf, ok := a.plat.(interface{ Preflight() error }); ok {
		if err := pf.Preflight(); err != nil {
			fmt.Fprintln(os.Stderr, "запуск:", err)
			logf("preflight: %v", err)
			return
		}
	}

	ln, err := ipc.Listen()
	if err != nil {
		// Without IPC the hotkey command cannot reach the daemon; refuse to
		// start a half-working second copy.
		fmt.Fprintln(os.Stderr, "ipc:", err)
		logf("ipc listen: %v", err)
		return
	}
	stop := ipc.Serve(ln, a.handleIPC)
	defer stop()
	logf("ipc listening on %s", ipc.Addr())

	srv := server.New(a.store, a.eng, server.Hooks{
		ApplyHotkeys: func() error {
			a.invalidateStatus()
			if pr, ok := a.plat.(interface{ ApplyHotkeys(*app) error }); ok {
				return pr.ApplyHotkeys(a)
			}
			return nil
		},
		Status: a.status,
		Toggle: a.toggle,
		Quit:   a.shutdown,
		RefreshTray: func() {
			a.invalidateStatus()
			a.refreshIcon()
		},
		EnableTray:   a.plat.EnableTray,
		Version:      Version,
		HotkeySource: cfg.Source,
	})
	addr, err := srv.Listen()
	if err != nil {
		fmt.Fprintln(os.Stderr, "http:", err)
		a.addr = cfg.HTTPAddr
	} else {
		a.addr = addr
	}

	if err := a.plat.Start(a); err != nil {
		// A second copy on Windows, or a broken hidden window, must not leave
		// a zombie daemon: HTTP and IPC are already up, so stop the process.
		fmt.Fprintln(os.Stderr, "запуск:", err)
		logf("start: %v", err)
		return
	}

	fmt.Printf("janus %s запущен\n  настройки: %s\n  конфиг:    %s\n  журнал:    %s\n",
		Version, a.uiURL(), a.store.FilePath(), logPath())
	logf("daemon started, ui=%s", a.uiURL())

	if a.store.Get().OpenUIOnStart {
		a.openUI()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-a.quit:
	}
	// Stop accepting hotkey commands first, then flush the clipboard and tear
	// the platform down. A command that arrived just before the listener closed
	// may still be running; Engine.Close waits for it under e.run.
	stop()
	a.eng.Close()
	a.plat.Stop()
	logf("daemon stopped")
}

// execute runs one action, used by the IPC handler and by the Windows hotkeys.
func (a *app) execute(slot string) string {
	cfg := a.store.Get()
	action := cfg.ActionMain
	if slot == "alt" {
		action = cfg.ActionAlt
	}
	ev, err := a.eng.Run(action)
	if err != nil {
		logf("action %s failed: %v", slot, err)
		return "err: " + err.Error()
	}
	logf("action %s ok: %q -> %q", slot, ev.Input, ev.Output)
	return "ok: " + ev.Output
}

func (a *app) handleIPC(cmd string) string {
	switch cmd {
	case "action main", "action alt":
		return a.execute(strings.TrimPrefix(cmd, "action "))
	case "toggle":
		return fmt.Sprintf("enabled=%v", a.toggle())
	case "status":
		return fmt.Sprintf("enabled=%v", a.store.Get().Enabled)
	case "reload":
		a.invalidateStatus()
		return "ok"
	}
	return "err: unknown command " + cmd
}

// ---------------------------------------------------------------- commands

func runAction() {
	slot := "main"
	if len(os.Args) > 2 && os.Args[2] == "alt" {
		slot = "alt"
	}
	if ans, err := ipc.Send("action " + slot); err == nil {
		if strings.HasPrefix(ans, "err") && os.Getenv("JANUS_QUIET") == "" {
			fmt.Fprintln(os.Stderr, ans)
		}
		return
	}
	// No daemon: act on our own, a little slower, but the hotkey still works.
	a := newApp()
	if _, err := a.eng.Run(a.store.Get().ActionFor(slot)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runInstall() {
	a := newApp()
	if pr, ok := a.plat.(interface{ ApplyHotkeys(*app) error }); ok {
		if err := pr.ApplyHotkeys(a); err != nil {
			fmt.Fprintln(os.Stderr, "горячие клавиши:", err)
		} else if a.plat.HotkeysManaged() {
			fmt.Println("горячие клавиши зарегистрирует демон, отдельная настройка не нужна")
		} else {
			fmt.Println("горячие клавиши прописаны в системе")
		}
	}
	if path, err := a.plat.InstallAutostart(); err != nil {
		fmt.Fprintln(os.Stderr, "автозапуск:", err)
	} else {
		fmt.Println("автозапуск добавлен:", path)
	}
	fmt.Println("настройки:", "http://"+a.store.Get().HTTPAddr+"/")
}

func runUninstall() {
	a := newApp()
	if pr, ok := a.plat.(interface{ RemoveHotkeys(*app) error }); ok {
		if err := pr.RemoveHotkeys(a); err != nil {
			fmt.Fprintln(os.Stderr, "горячие клавиши:", err)
		} else {
			fmt.Println("горячие клавиши убраны")
		}
	}
	if err := a.plat.RemoveAutostart(); err != nil {
		fmt.Fprintln(os.Stderr, "автозапуск:", err)
	} else {
		fmt.Println("автозапуск убран")
	}
	if ans, err := ipc.Send("status"); err == nil {
		fmt.Println("демон работает:", ans)
	}
}

func runStatus() {
	a := newApp()
	fmt.Println("janus " + Version + " (" + runtimeName() + ")")
	fmt.Println("конфиг:  ", a.store.FilePath())
	fmt.Println("журнал:  ", logPath())
	fmt.Println("бинарник:", exePath())
	cfg := a.store.Get()
	fmt.Printf("включён:  %v\nисточник: %s\nклавиши:  %s / %s\n",
		cfg.Enabled, cfg.Source, cfg.HotkeyMain, cfg.HotkeyAlt)
	if ans, err := ipc.Send("status"); err == nil {
		fmt.Println("демон:    работает (" + ans + ")")
	} else {
		fmt.Println("демон:    не запущен")
	}
	st := a.status()
	for _, k := range sortedKeys(st) {
		fmt.Printf("%-20s %v\n", k+":", st[k])
	}
}

func runDiag() {
	a := newApp()
	fmt.Println("janus " + Version + " (" + runtimeName() + ")")
	fmt.Printf("PID:      %d\n", os.Getpid())
	fmt.Println("конфиг:  ", a.store.FilePath())
	fmt.Println("журнал:  ", logPath())
	fmt.Println("бинарник:", exePath())
	if ans, err := ipc.Send("status"); err == nil {
		fmt.Println("демон:    работает (" + ans + ")")
	} else {
		fmt.Println("демон:    не запущен")
	}
	fmt.Println("--- настройки ---")
	cfg := a.store.Get()
	fmt.Printf("включён=%v источник=%s main=%s alt=%s действие1=%s действие2=%s\n",
		cfg.Enabled, cfg.Source, cfg.HotkeyMain, cfg.HotkeyAlt, cfg.ActionMain, cfg.ActionAlt)
	fmt.Println("--- платформа ---")
	for _, line := range a.plat.Diag() {
		fmt.Println(line)
	}
	fmt.Println("--- проверка переворота ---")
	for _, in := range []string{"ghbdtn vjq lheu", "руддщ", "Ghbdtn!"} {
		out, dir := engine.Preview(in)
		fmt.Printf("%-18q -> %-18q [%s]\n", in, out, dir)
	}
	fmt.Println("--- итог ---")
	ok := 0
	total := 0
	for _, v := range a.status() {
		if b, isBool := v.(bool); isBool {
			total++
			if b {
				ok++
			}
		}
	}
	fmt.Printf("проверок пройдено: %d из %d\n", ok, total)
}

func runConvert() {
	text := strings.Join(os.Args[2:], " ")
	if text == "" {
		sc := bufio.NewScanner(os.Stdin)
		var lines []string
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		text = strings.Join(lines, "\n")
	}
	out, dir := engine.Preview(text)
	if os.Getenv("JANUS_VERBOSE") != "" {
		fmt.Fprintf(os.Stderr, "%s\n", dir)
	}
	fmt.Print(out)
}

func runUI() {
	a := newApp()
	url := "http://" + a.store.Get().HTTPAddr + "/"
	if ans, err := ipc.Send("status"); err == nil {
		fmt.Println("демон работает:", ans)
	} else {
		fmt.Println("демон не запущен, открываю сохранённый адрес")
	}
	if err := a.plat.OpenURL(url); err != nil {
		fmt.Println("откройте вручную:", url)
	}
}

func runExtension() {
	a := newApp()
	if err := a.plat.EnableTray(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("значки в трее включены")
}
