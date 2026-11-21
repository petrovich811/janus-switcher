//go:build linux

// Package layout reads and switches the system keyboard layout.
//
// On GNOME the "current" key of org.gnome.desktop.input-sources is written by
// the shell but not read back, so setting it does nothing. What does work is
// (a) the most-recently-used list, which the shell keeps in switching order and
// which therefore names the active layout, and (b) the shell's own
// "switch input source" shortcut, which we press through the virtual keyboard.
package layout

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/petrovich811/janus-switcher/internal/inject"
)

const sourcesSchema = "org.gnome.desktop.input-sources"

// Source is one configured input source.
type Source struct {
	Index  int
	Type   string
	Layout string // "ru", "us", ... (the part before any "+" or "_")
	Raw    string
}

var tuple = regexp.MustCompile("\\('([^']*)',\\s*'([^']*)'\\)")

func run(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func parseSources(out string) []Source {
	var res []Source
	for i, m := range tuple.FindAllStringSubmatch(out, -1) {
		raw := m[2]
		short := raw
		if j := strings.IndexAny(short, "+_"); j > 0 {
			short = short[:j]
		}
		res = append(res, Source{Index: i, Type: m[1], Layout: short, Raw: raw})
	}
	return res
}

// The settings are read through gsettings, which costs a process per call. A
// very short cache keeps status polling and the hotkey path quiet without
// hiding a real layout change: SwitchTo invalidates it explicitly.
var (
	cacheMu  sync.Mutex
	cacheAt  time.Time
	cacheTTL = 400 * time.Millisecond
	cacheSrc []Source
	cacheMRU []Source
	cacheOK  bool
)

func invalidate() {
	cacheMu.Lock()
	cacheOK = false
	cacheMu.Unlock()
}

func cached() (srcs, mru []Source, ok bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if cacheOK && time.Since(cacheAt) < cacheTTL {
		return cacheSrc, cacheMRU, true
	}
	return nil, nil, false
}

func refresh() (srcs, mru []Source, err error) {
	out, err := run("gsettings", "get", sourcesSchema, "sources")
	if err != nil {
		return nil, nil, err
	}
	srcs = parseSources(out)
	out, err = run("gsettings", "get", sourcesSchema, "mru-sources")
	if err != nil {
		return srcs, nil, err
	}
	mru = parseSources(out)
	cacheMu.Lock()
	cacheSrc, cacheMRU, cacheAt, cacheOK = srcs, mru, time.Now(), true
	cacheMu.Unlock()
	return srcs, mru, nil
}

// Sources lists the configured layouts in switching order.
func Sources() ([]Source, error) {
	if srcs, _, ok := cached(); ok {
		return srcs, nil
	}
	srcs, _, err := refresh()
	return srcs, err
}

// MRU lists the layouts in most-recently-used order: the first one is active.
func MRU() ([]Source, error) {
	if _, mru, ok := cached(); ok {
		return mru, nil
	}
	_, mru, err := refresh()
	return mru, err
}

// Active returns the layout currently in use ("ru", "us", ...).
func Active() (string, error) {
	srcs, err := MRU()
	if err != nil {
		return "", err
	}
	if len(srcs) == 0 {
		return "", fmt.Errorf("no input sources configured")
	}
	return srcs[0].Layout, nil
}

// Find returns the source index whose layout matches want.
func Find(want string) (int, bool) {
	srcs, err := Sources()
	if err != nil {
		return 0, false
	}
	for _, s := range srcs {
		if strings.EqualFold(s.Layout, want) {
			return s.Index, true
		}
	}
	return 0, false
}

// switchChord reads the user's own "switch input source" shortcut.
func switchChord() ([]uint16, uint16, error) {
	out, err := run("gsettings", "get", "org.gnome.desktop.wm.keybindings", "switch-input-source")
	if err == nil {
		if m := regexp.MustCompile("'([^']+)'").FindStringSubmatch(out); m != nil {
			if mods, code, err := inject.ParseShortcut(m[1]); err == nil {
				return mods, code, nil
			}
		}
	}
	// Fall back to the GNOME default.
	return inject.ParseShortcut("super+space")
}

// GNOME switches layouts through the session's own shortcut.
type GNOME struct{}

// Active reports the layout currently in use.
func (GNOME) Active() (string, error) { return Active() }

// SwitchTo makes the wanted layout active, verifying the result.
func (GNOME) SwitchTo(kb inject.Sender, want string) error { return SwitchTo(kb, want) }

// Summary renders the configured layouts for diagnostics.
func (GNOME) Summary() string { return LayoutListOf() }

// Available reports whether the layouts can be read at all.
func (GNOME) Available() error {
	_, err := MRU()
	return err
}

// SwitchTo makes the layout with the given prefix active, using the system's
// own switching shortcut. It verifies the result and gives up after a few tries.
func SwitchTo(kb inject.Sender, want string) error {
	if kb == nil {
		return fmt.Errorf("no keyboard available to switch layouts")
	}
	cur, err := Active()
	if err != nil {
		return err
	}
	if strings.EqualFold(cur, want) {
		return nil
	}
	mods, code, err := switchChord()
	if err != nil {
		return fmt.Errorf("cannot read the layout switching shortcut: %w", err)
	}
	for i := 0; i < 3; i++ {
		if err := kb.Chord(mods, code); err != nil {
			return err
		}
		for j := 0; j < 4; j++ {
			sleep()
			invalidate()
			if now, err := Active(); err == nil && strings.EqualFold(now, want) {
				return nil
			}
		}
	}
	now, _ := Active()
	return fmt.Errorf("could not switch layout from %q to %q", now, want)
}

// SwitchingSupported reports whether we can change the layout at all.
func SwitchingSupported() bool {
	_, err := MRU()
	return err == nil
}

// LayoutListOf renders the configured layouts as "ru, us" for diagnostics.
func LayoutListOf() string {
	srcs, err := Sources()
	if err != nil {
		return ""
	}
	var names []string
	for _, s := range srcs {
		names = append(names, s.Layout)
	}
	return strings.Join(names, ", ")
}
