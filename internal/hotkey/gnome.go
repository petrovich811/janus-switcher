//go:build linux

package hotkey

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	mediaKeysSchema = "org.gnome.settings-daemon.plugins.media-keys"
	keybindingBase  = "org.gnome.settings-daemon.plugins.media-keys.custom-keybinding"
	pathPrefix      = "/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/"
)

// GNOME manages global shortcuts through GNOME custom keybindings: a dconf
// list of paths, each holding a name, a command and a binding. GNOME Shell
// grabs those bindings itself, so they work on Wayland and on X11 and need no
// privileges — the command is simply spawned when the keys are pressed.
type GNOME struct {
	// Binary is the executable the shortcuts call.
	Binary string
}

// Binding is one GNOME custom shortcut entry.
type Binding struct {
	Path    string
	Name    string
	Command string
	Binding string
}

// Path returns the dconf path used for a slot ("main" or "alt").
func Path(slot string) string { return pathPrefix + "janus-" + slot + "/" }

func gsettings(args ...string) (string, error) {
	var out, errb bytes.Buffer
	cmd := exec.Command("gsettings", args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("gsettings %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Available reports whether a GNOME session with gsettings is reachable.
func Available() bool {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return false
	}
	if _, err := gsettings("get", mediaKeysSchema, "custom-keybindings"); err != nil {
		return false
	}
	return true
}

var quoted = regexp.MustCompile("'([^']*)'")

// paths reads the current list of custom keybinding paths.
func paths() ([]string, error) {
	out, err := gsettings("get", mediaKeysSchema, "custom-keybindings")
	if err != nil {
		return nil, err
	}
	var res []string
	for _, m := range quoted.FindAllStringSubmatch(out, -1) {
		res = append(res, m[1])
	}
	return res, nil
}

func setPaths(list []string) error {
	quotedList := make([]string, 0, len(list))
	for _, p := range list {
		quotedList = append(quotedList, "'"+p+"'")
	}
	val := "[" + strings.Join(quotedList, ", ") + "]"
	_, err := gsettings("set", mediaKeysSchema, "custom-keybindings", val)
	return err
}

func hasPath(list []string, p string) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

// Install writes (or updates) our shortcuts, keeping the user's own.
func (gh *GNOME) Install(main, alt Binding) error {
	list, err := paths()
	if err != nil {
		return err
	}
	for _, b := range []Binding{main, alt} {
		if !hasPath(list, b.Path) {
			list = append(list, b.Path)
		}
	}
	if err := setPaths(list); err != nil {
		return err
	}
	for _, b := range []Binding{main, alt} {
		schema := keybindingBase + ":" + b.Path
		if b.Name == "" {
			b.Name = "Janus"
		}
		if _, err := gsettings("set", schema, "name", b.Name); err != nil {
			return err
		}
		if _, err := gsettings("set", schema, "command", b.Command); err != nil {
			return err
		}
		if _, err := gsettings("set", schema, "binding", b.Binding); err != nil {
			return err
		}
	}
	return nil
}

// Remove deletes our shortcuts from the list.
func (gh *GNOME) Remove(slots ...string) error {
	list, err := paths()
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, s := range slots {
		drop[Path(s)] = true
	}
	var kept []string
	for _, p := range list {
		if !drop[p] {
			kept = append(kept, p)
		}
	}
	return setPaths(kept)
}

// Current reads back what is actually stored, for diagnostics.
func (gh *GNOME) Current(slot string) (Binding, error) {
	p := Path(slot)
	list, err := paths()
	if err != nil {
		return Binding{}, err
	}
	if !hasPath(list, p) {
		return Binding{Path: p}, fmt.Errorf("shortcut %q is not registered", slot)
	}
	schema := keybindingBase + ":" + p
	name, _ := gsettings("get", schema, "name")
	cmd, _ := gsettings("get", schema, "command")
	bind, _ := gsettings("get", schema, "binding")
	return Binding{
		Path:    p,
		Name:    strings.Trim(name, "'"),
		Command: strings.Trim(cmd, "'"),
		Binding: strings.Trim(bind, "'"),
	}, nil
}

// ExtensionEnabled reports whether the AppIndicator extension that draws tray
// icons is active.
func ExtensionEnabled() bool {
	out, err := gsettings("get", "org.gnome.shell", "enabled-extensions")
	if err != nil {
		return false
	}
	return strings.Contains(out, "ubuntu-appindicators") || strings.Contains(out, "appindicatorsupport")
}

// EnableExtension turns on the tray icon extension Ubuntu ships.
func EnableExtension() error {
	for _, id := range []string{"ubuntu-appindicators@ubuntu.com", "appindicatorsupport@rgcjonas.gmail.com"} {
		if _, err := exec.Command("gnome-extensions", "enable", id).CombinedOutput(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("could not enable the AppIndicator extension")
}
