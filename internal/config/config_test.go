package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesOldPauseDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", dir)
	old := Config{
		Enabled:    true,
		Source:     SourceGNOME,
		HotkeyMain: "<Control><Shift>Pause",
		HotkeyAlt:  "<Shift>Pause",
		ActionMain: ActionSelection,
		ActionAlt:  ActionWord,
		HTTPAddr:   "127.0.0.1:8791",
		ShowTray:   true,
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	if cfg.HotkeyMain != "<Control><Shift>F10" {
		t.Errorf("main = %q, want the F10 default", cfg.HotkeyMain)
	}
	if cfg.HotkeyAlt != "<Control><Alt>F10" {
		t.Errorf("alt = %q, want the Alt+F10 default", cfg.HotkeyAlt)
	}
	if cfg.DefaultsVersion != defaultsVersion {
		t.Errorf("defaults version = %d, want %d", cfg.DefaultsVersion, defaultsVersion)
	}

	// The migration must be persisted, otherwise the next start would repeat it.
	data, err = os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.HotkeyMain != cfg.HotkeyMain || saved.DefaultsVersion != defaultsVersion {
		t.Errorf("saved config = %+v", saved)
	}
}

func TestLoadKeepsConsciousPauseAfterMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", dir)
	cfg := Default()
	cfg.DefaultsVersion = defaultsVersion
	cfg.HotkeyMain = "<Control><Shift>Pause"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().HotkeyMain; got != "<Control><Shift>Pause" {
		t.Errorf("main = %q; a versioned config must keep Pause", got)
	}
}
