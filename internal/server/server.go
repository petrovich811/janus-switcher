// Package server serves the local settings page and its JSON API.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/convert"
	"github.com/petrovich811/janus-switcher/internal/engine"
	"github.com/petrovich811/janus-switcher/internal/hotkey"
)

//go:embed ui/index.html
var uiFS embed.FS

// Hooks are the daemon operations the web interface can trigger.
type Hooks struct {
	ApplyHotkeys func() error
	Status       func() map[string]any
	Toggle       func() bool
	Quit         func()
	RefreshTray  func()
	EnableTray   func() error
	Version      string
	HotkeySource config.HotkeySource
}

// Server is the settings interface.
type Server struct {
	store *config.Store
	eng   *engine.Engine
	hooks Hooks
}

// New creates the server.
func New(store *config.Store, eng *engine.Engine, hooks Hooks) *Server {
	return &Server{store: store, eng: eng, hooks: hooks}
}

// Handler builds the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/config", s.saveConfig)
	mux.HandleFunc("/api/hotkey", s.saveHotkey)
	mux.HandleFunc("/api/test", s.test)
	mux.HandleFunc("/api/action", s.action)
	mux.HandleFunc("/api/log", s.logEvents)
	mux.HandleFunc("/api/tray", s.tray)
	mux.HandleFunc("/api/quit", s.quit)
	return mux
}

// Listen starts the HTTP server on the configured address.
//
// A restart right after the previous daemon exited can find the port still
// taken, so we retry for a few seconds and finally fall back to any free port.
func (s *Server) Listen() (string, error) {
	addr := s.store.Get().HTTPAddr
	var ln net.Listener
	var err error
	for i := 0; i < 25; i++ {
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "настройки на %s: %v\n", ln.Addr(), err)
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), nil
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// guard rejects state changing requests that a random web page could send.
func guard(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return false
	}
	if r.Header.Get("X-Janus") != "1" {
		http.Error(w, "missing X-Janus header", http.StatusForbidden)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"config":  s.store.Get(),
		"status":  s.hooks.Status(),
		"version": s.hooks.Version,
		"log":     s.eng.Log(),
	})
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var in config.Config
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := s.store.Update(func(c *config.Config) {
		in.HTTPAddr = c.HTTPAddr
		*c = in
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	applyErr := ""
	if s.hooks.ApplyHotkeys != nil {
		if err := s.hooks.ApplyHotkeys(); err != nil {
			applyErr = err.Error()
		}
	}
	if s.hooks.RefreshTray != nil {
		s.hooks.RefreshTray()
	}
	writeJSON(w, map[string]any{"ok": true, "config": cfg, "apply_error": applyErr, "status": s.hooks.Status()})
}

func (s *Server) saveHotkey(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var in struct {
		Slot  string `json:"slot"`
		Combo string `json:"combo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	combo, err := hotkey.Parse(in.Combo)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !combo.HasModifier() && !combo.IsFunctionKey() {
		writeJSON(w, map[string]any{"ok": false,
			"error": "глобальное сочетание должно содержать модификатор (Ctrl, Alt, Super или Shift) либо быть функциональной клавишей"})
		return
	}
	accel, err := combo.Accelerator()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_, err = s.store.Update(func(c *config.Config) {
		if in.Slot == "alt" {
			c.HotkeyAlt = accel
		} else {
			c.HotkeyMain = accel
		}
	})
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if s.hooks.ApplyHotkeys != nil {
		if err := s.hooks.ApplyHotkeys(); err != nil {
			writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "combo": combo.String()})
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "accelerator": accel, "combo": combo.String()})
}

func (s *Server) test(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	out, dir := engine.Preview(in.Text)
	res := map[string]any{"out": out, "dir": dir}
	cfg := s.store.Get()
	if cfg.SwitchLayoutAfter {
		if _, to := convert.Detect(in.Text).Layouts(); to == convert.LayoutRU {
			res["layout"] = "ru"
		} else {
			res["layout"] = "us"
		}
	}
	writeJSON(w, res)
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a := config.Action(strings.TrimSpace(in.Action))
	switch a {
	case config.ActionSelection, config.ActionWord, config.ActionLine:
	default:
		a = config.ActionSelection
	}
	ev, err := s.eng.Run(a)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	writeJSON(w, map[string]any{"ok": err == nil, "event": ev, "error": msg})
}

func (s *Server) logEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.eng.Log())
}

func (s *Server) tray(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	if s.hooks.EnableTray == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "not supported"})
		return
	}
	if err := s.hooks.EnableTray(); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		time.Sleep(300 * time.Millisecond)
		if s.hooks.Quit != nil {
			s.hooks.Quit()
		}
	}()
}

var _ = fmt.Sprintf
