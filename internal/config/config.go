// Package config stores user settings in a small JSON file.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Action is what a hotkey does with the text around the caret.
type Action string

const (
	// ActionSelection converts whatever is selected, falling back to the last
	// typed word when nothing is selected.
	ActionSelection Action = "selection"
	// ActionWord converts the word just before the caret.
	ActionWord Action = "word"
	// ActionLine converts the current line.
	ActionLine Action = "line"
)

// HotkeySource says who watches the keyboard.
type HotkeySource string

const (
	// SourceGNOME uses GNOME custom shortcuts: works on Wayland and X11 and
	// needs no privileges. The shortcuts are managed by the daemon.
	SourceGNOME HotkeySource = "gnome"
	// SourceOff leaves the keyboard alone: the user binds "janus action main"
	// in their own desktop settings.
	SourceOff HotkeySource = "off"
)

// Config is the whole on-disk configuration.
type Config struct {
	Enabled bool `json:"enabled"`

	// Bindings are GTK accelerators such as "<Control><Shift>Pause".
	Source     HotkeySource `json:"hotkey_source"`
	HotkeyMain string       `json:"hotkey_main"`
	HotkeyAlt  string       `json:"hotkey_alt"`

	ActionMain Action `json:"action_main"`
	ActionAlt  Action `json:"action_alt"`

	// SwitchLayoutAfter selects the system layout matching the converted text.
	SwitchLayoutAfter bool `json:"switch_layout_after"`
	// RestoreClipboard puts the previous clipboard text back afterwards.
	RestoreClipboard bool `json:"restore_clipboard"`
	// PasteDelayMs is how long the converted text stays on the clipboard before
	// Ctrl+V is sent.
	PasteDelayMs int `json:"paste_delay_ms"`
	// ReleaseDelayMs is how long to wait for the user to let go of the hotkey
	// modifiers before sending our own chords. On Windows the real key state is
	// polled and this is only an upper bound.
	ReleaseDelayMs int `json:"release_delay_ms"`

	// CopyShortcut and PasteShortcut are the chords used to talk to the focused
	// application: "ctrl+c", "ctrl+insert", "ctrl+shift+c" and so on.
	CopyShortcut  string `json:"copy_shortcut"`
	PasteShortcut string `json:"paste_shortcut"`

	// HTTPAddr is the address of the local settings interface.
	HTTPAddr string `json:"http_addr"`
	// OpenUIOnStart opens the settings page in a browser at startup.
	OpenUIOnStart bool `json:"open_ui_on_start"`
	// ShowTray draws the tray icon.
	ShowTray bool `json:"show_tray"`
}

// Default returns the configuration used on first start.
func Default() Config {
	return Config{
		Enabled:           true,
		Source:            SourceGNOME,
		HotkeyMain:        "<Control><Shift>Pause",
		HotkeyAlt:         "<Shift>Pause",
		ActionMain:        ActionSelection,
		ActionAlt:         ActionWord,
		SwitchLayoutAfter: true,
		RestoreClipboard:  false,
		PasteDelayMs:      120,
		ReleaseDelayMs:    200,
		CopyShortcut:      "ctrl+c",
		PasteShortcut:     "ctrl+v",
		HTTPAddr:          "127.0.0.1:8791",
		ShowTray:          true,
	}
}

// Store reads and writes the configuration file under a mutex.
type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

// Dir is the configuration directory (~/.config/janus).
func Dir() string {
	if d := os.Getenv("JANUS_CONFIG_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "janus")
}

// Path is the full path of the configuration file.
func Path() string { return filepath.Join(Dir(), "config.json") }

// migrateLegacy carries the settings over from the directory used before the
// program was renamed, so that nobody loses their hotkeys on upgrade.
func migrateLegacy() {
	base, err := os.UserConfigDir()
	if err != nil {
		return
	}
	legacy := filepath.Join(base, "punto")
	data, err := os.ReadFile(filepath.Join(legacy, "config.json"))
	if err != nil {
		return
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(Dir(), "config.json"), data, 0o644)
	if logData, err := os.ReadFile(filepath.Join(legacy, "punto.log")); err == nil {
		_ = os.WriteFile(filepath.Join(Dir(), "janus.log"), logData, 0o644)
	}
}

// Load opens the store, writing defaults when the file is missing.
func Load() (*Store, error) {
	s := &Store{path: Path(), cfg: Default()}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		migrateLegacy()
		data, err = os.ReadFile(s.path)
		if errors.Is(err, fs.ErrNotExist) {
			return s, s.save()
		}
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return s, err
	}
	s.normalize()
	return s, nil
}

func (s *Store) normalize() {
	d := Default()
	if s.cfg.Source == "" {
		s.cfg.Source = d.Source
	}
	if s.cfg.HotkeyMain == "" {
		s.cfg.HotkeyMain = d.HotkeyMain
	}
	if s.cfg.HotkeyAlt == "" {
		s.cfg.HotkeyAlt = d.HotkeyAlt
	}
	if s.cfg.ActionMain == "" {
		s.cfg.ActionMain = d.ActionMain
	}
	if s.cfg.ActionAlt == "" {
		s.cfg.ActionAlt = d.ActionAlt
	}
	if s.cfg.CopyShortcut == "" {
		s.cfg.CopyShortcut = d.CopyShortcut
	}
	if s.cfg.PasteShortcut == "" {
		s.cfg.PasteShortcut = d.PasteShortcut
	}
	if s.cfg.HTTPAddr == "" {
		s.cfg.HTTPAddr = d.HTTPAddr
	}
	if s.cfg.PasteDelayMs <= 0 {
		s.cfg.PasteDelayMs = d.PasteDelayMs
	}
	if s.cfg.ReleaseDelayMs <= 0 {
		s.cfg.ReleaseDelayMs = d.ReleaseDelayMs
	}
}

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update applies fn to the configuration and saves it.
func (s *Store) Update(fn func(*Config)) (Config, error) {
	s.mu.Lock()
	fn(&s.cfg)
	s.normalize()
	cfg := s.cfg
	s.mu.Unlock()
	return cfg, s.save()
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.mu.RLock()
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}

// FilePath is the location of the configuration file.
func (s *Store) FilePath() string { return s.path }

// ActionFor returns the action bound to a hotkey slot ("main" or "alt").
func (c Config) ActionFor(slot string) Action {
	if slot == "alt" {
		return c.ActionAlt
	}
	return c.ActionMain
}
