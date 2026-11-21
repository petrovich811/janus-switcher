// Package engine performs the actual conversion of the text around the caret.
//
// It knows nothing about the platform: the keyboard, the clipboard and the
// layout switcher are interfaces implemented for Linux and for Windows.
package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/convert"
	"github.com/petrovich811/janus-switcher/internal/inject"
)

// Keyboard injects keystrokes into the focused window.
type Keyboard interface {
	Chord(mods []uint16, key uint16) error
	Tap(key uint16) error
	TypeText(text string, layout convert.LayoutID) error
	// WaitModifiersReleased blocks until the user lets go of Ctrl/Shift/Alt,
	// so that our own chords are not mixed with the hotkey that started us.
	WaitModifiersReleased(timeout time.Duration) error
	Close() error
}

// Clipboard reads and writes the system clipboard.
type Clipboard interface {
	ReadText() (string, error)
	WriteText(text string) error
	// BeginCopy marks the moment "the application is about to copy" and returns
	// an opaque token. Linux puts a probe on the clipboard; Windows only records
	// the clipboard sequence number, which keeps the user's clipboard and the
	// Win+V history clean.
	BeginCopy() string
	// WaitCopied returns the text once a copy really happened.
	WaitCopied(token string, timeout time.Duration) (string, bool)
	Close()
}

// Layouts reports and changes the active keyboard layout.
type Layouts interface {
	Active() (string, error)
	SwitchTo(kb inject.Sender, want string) error
	Summary() string
	Available() error
}

// Event is one recorded conversion, shown in the settings interface.
type Event struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	Input  string `json:"input"`
	Output string `json:"output"`
	Dir    string `json:"dir"`
	Err    string `json:"err,omitempty"`
}

// Engine ties the platform pieces together and keeps the action log.
type Engine struct {
	store *config.Store
	kb    Keyboard
	clip  Clipboard
	lays  Layouts

	// Logf receives diagnostics; nil is fine.
	Logf func(format string, args ...any)

	run  sync.Mutex
	evMu sync.Mutex
	evts []Event
}

// New creates an engine over the platform implementations.
func New(store *config.Store, kb Keyboard, clip Clipboard, lays Layouts) *Engine {
	return &Engine{store: store, kb: kb, clip: clip, lays: lays}
}

func (e *Engine) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Log returns the recorded events, newest first.
func (e *Engine) Log() []Event {
	e.evMu.Lock()
	defer e.evMu.Unlock()
	out := make([]Event, len(e.evts))
	for i := range e.evts {
		out[len(e.evts)-1-i] = e.evts[i]
	}
	return out
}

func (e *Engine) record(ev Event) {
	e.evMu.Lock()
	defer e.evMu.Unlock()
	e.evts = append(e.evts, ev)
	if len(e.evts) > 100 {
		e.evts = e.evts[len(e.evts)-100:]
	}
}

// Run converts the text around the caret according to the given action.
func (e *Engine) Run(a config.Action) (Event, error) {
	e.run.Lock()
	defer e.run.Unlock()

	cfg := e.store.Get()
	ev := Event{Time: time.Now().Format("15:04:05"), Action: string(a)}
	if !cfg.Enabled {
		ev.Err = "выключено"
		e.record(ev)
		return ev, nil
	}

	copyMods, copyCode, err := inject.ParseShortcut(cfg.CopyShortcut)
	if err != nil {
		ev.Err = "copy shortcut: " + err.Error()
		e.record(ev)
		return ev, err
	}
	pasteMods, pasteCode, err := inject.ParseShortcut(cfg.PasteShortcut)
	if err != nil {
		ev.Err = "paste shortcut: " + err.Error()
		e.record(ev)
		return ev, err
	}

	// The hotkey may still be held down: otherwise Ctrl+C becomes Ctrl+Shift+C.
	if err := e.kb.WaitModifiersReleased(time.Duration(cfg.ReleaseDelayMs) * time.Millisecond); err != nil {
		e.logf("modifier wait: %v", err)
		ev.Err = err.Error()
		e.record(ev)
		return ev, err
	}

	before, _ := e.clip.ReadText()
	e.logf("action %s, clipboard before=%q", a, before)
	text, ok := e.grab(a, before, copyMods, copyCode)
	e.logf("grabbed %q ok=%v", text, ok)
	if !ok {
		// Nothing was copied, so nothing was changed: BeginCopy on Windows does
		// not touch the clipboard at all.
		e.logf("nothing to convert, clipboard left as it was")
		ev.Err = "нечего конвертировать: нет выделения и не удалось выделить слово"
		e.record(ev)
		return ev, fmt.Errorf("%s", ev.Err)
	}
	ev.Input = text

	dir := convert.Detect(text)
	out := convert.SwapDir(text, dir)
	ev.Output = out
	if dir == convert.ToEN {
		ev.Dir = "ru→en"
	} else {
		ev.Dir = "en→ru"
	}
	if out == text || out == "" {
		ev.Err = "текст не изменился"
		e.record(ev)
		return ev, nil
	}

	if err := e.clip.WriteText(out); err != nil {
		ev.Err = "clipboard: " + err.Error()
		e.record(ev)
		return ev, err
	}
	time.Sleep(time.Duration(cfg.PasteDelayMs) * time.Millisecond)
	if err := e.kb.Chord(pasteMods, pasteCode); err != nil {
		ev.Err = "paste: " + err.Error()
		e.record(ev)
		return ev, err
	}

	if cfg.SwitchLayoutAfter && e.lays != nil {
		_, to := dir.Layouts()
		if err := e.lays.SwitchTo(e.kb, string(to)); err != nil {
			e.logf("layout switch failed: %v", err)
		} else {
			e.logf("layout switched to %s", to)
		}
	}
	if cfg.RestoreClipboard && before != "" {
		go func() {
			time.Sleep(700 * time.Millisecond)
			_ = e.clip.WriteText(before)
		}()
	}
	e.record(ev)
	return ev, nil
}

// grab puts the text we want to convert on the clipboard and returns it.
func (e *Engine) grab(a config.Action, before string, copyMods []uint16, copyCode uint16) (string, bool) {
	if a == config.ActionSelection {
		token := e.clip.BeginCopy()
		if err := e.kb.Chord(copyMods, copyCode); err == nil {
			if text, ok := e.clip.WaitCopied(token, 500*time.Millisecond); ok {
				return text, true
			}
		}
		e.logf("no selection copied, falling back to the word")
	}
	switch a {
	case config.ActionLine:
		_ = e.kb.Tap(inject.KeyHome)
		_ = e.kb.Chord([]uint16{inject.KeyLeftShift}, inject.KeyEnd)
	case config.ActionWord, config.ActionSelection:
		// Collapse any selection, then take the word before the caret.
		_ = e.kb.Tap(inject.KeyRight)
		_ = e.kb.Chord([]uint16{inject.KeyLeftCtrl, inject.KeyLeftShift}, inject.KeyLeft)
	}
	time.Sleep(60 * time.Millisecond)
	token := e.clip.BeginCopy()
	if err := e.kb.Chord(copyMods, copyCode); err != nil {
		return "", false
	}
	return e.clip.WaitCopied(token, 700*time.Millisecond)
}

// Preview converts text without touching the clipboard, for the settings UI.
func Preview(text string) (string, string) {
	dir := convert.Detect(text)
	out := convert.SwapDir(text, dir)
	if dir == convert.ToEN {
		return out, "ru→en"
	}
	return out, "en→ru"
}
