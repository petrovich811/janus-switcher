package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/petrovich811/janus-switcher/internal/config"
	"github.com/petrovich811/janus-switcher/internal/convert"
	"github.com/petrovich811/janus-switcher/internal/inject"
)

// fakeKeyboard records what the engine asked the keyboard to do.
type fakeKeyboard struct {
	chords   [][2]any // {mods, key}
	text     []string
	taps     []uint16
	waited   time.Duration
	closeCnt int
}

func (f *fakeKeyboard) Chord(mods []uint16, key uint16) error {
	f.chords = append(f.chords, [2]any{mods, key})
	return nil
}

func (f *fakeKeyboard) Tap(key uint16) error {
	f.taps = append(f.taps, key)
	return nil
}

func (f *fakeKeyboard) TypeText(text string, layout convert.LayoutID) error {
	f.text = append(f.text, text)
	return nil
}

func (f *fakeKeyboard) WaitModifiersReleased(timeout time.Duration) error {
	f.waited = timeout
	return nil
}

func (f *fakeKeyboard) Close() error { f.closeCnt++; return nil }

// The copy chord is the first one the engine sends; acknowledge it by "copying"
// the prepared selection into the fake clipboard.
func (f *fakeKeyboard) copyCount() int { return len(f.chords) }

type fakeClipboard struct {
	text     string
	written  []string
	copied   string // what the next copy will produce
	kb       *fakeKeyboard
	fail     error
	mark     int
	begins   int
	restored string
}

func (c *fakeClipboard) ReadText() (string, error) {
	if c.fail != nil {
		return "", c.fail
	}
	return c.text, nil
}

func (c *fakeClipboard) WriteText(text string) error {
	if c.fail != nil {
		return c.fail
	}
	c.text = text
	c.written = append(c.written, text)
	return nil
}

// RestoreAfterFailedCopy mirrors the Linux clipboard: after a failed grab the
// probe must be replaced by the text that was there before.
func (c *fakeClipboard) RestoreAfterFailedCopy(original string) {
	c.restored = original
	c.text = original
}

// BeginCopy marks the moment the engine starts asking for a copy.
func (c *fakeClipboard) BeginCopy() string {
	if c.kb != nil {
		c.mark = c.kb.copyCount()
	}
	c.begins++
	return "token"
}

// WaitCopied simulates the application copying the selection: once a copy chord
// was sent after BeginCopy, the clipboard holds the selection.
func (c *fakeClipboard) WaitCopied(token string, timeout time.Duration) (string, bool) {
	if c.copied != "" && c.kb != nil && c.kb.copyCount() > c.mark {
		c.text = c.copied
		return c.copied, true
	}
	return "", false
}

func (c *fakeClipboard) Close() {}

type fakeLayouts struct {
	switched []string
	err      error
}

func (l *fakeLayouts) Active() (string, error) { return "us", nil }
func (l *fakeLayouts) SwitchTo(kb inject.Sender, want string) error {
	l.switched = append(l.switched, want)
	return l.err
}
func (l *fakeLayouts) Summary() string  { return "ru, us" }
func (l *fakeLayouts) Available() error { return nil }

func newTestEngine(t *testing.T, cfg config.Config, copied string) (*Engine, *fakeKeyboard, *fakeClipboard, *fakeLayouts, *config.Store) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("JANUS_CONFIG_DIR", dir)
	store, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(func(c *config.Config) { *c = cfg }); err != nil {
		t.Fatal(err)
	}
	kb := &fakeKeyboard{}
	clip := &fakeClipboard{copied: copied, kb: kb}
	lays := &fakeLayouts{}
	e := New(store, kb, clip, lays)
	return e, kb, clip, lays, store
}

func baseConfig() config.Config {
	c := config.Default()
	c.ReleaseDelayMs = 10
	c.PasteDelayMs = 10
	return c
}

func TestRunConvertsSelection(t *testing.T) {
	e, kb, clip, lays, _ := newTestEngine(t, baseConfig(), "Ghbdtn vjq lheu")

	ev, err := e.Run(config.ActionSelection)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ev.Input != "Ghbdtn vjq lheu" {
		t.Errorf("input = %q", ev.Input)
	}
	if ev.Output != "Привет мой друг" {
		t.Errorf("output = %q", ev.Output)
	}
	if ev.Dir != "en→ru" {
		t.Errorf("dir = %q", ev.Dir)
	}
	// The converted text must be on the clipboard and a paste chord must follow.
	if clip.text != "Привет мой друг" {
		t.Errorf("clipboard = %q", clip.text)
	}
	if len(kb.chords) < 2 {
		t.Errorf("expected copy and paste chords, got %d", len(kb.chords))
	}
	if kb.waited != 10*time.Millisecond {
		t.Errorf("modifier wait = %v, want the configured 10ms", kb.waited)
	}
	if len(lays.switched) != 1 || lays.switched[0] != "ru" {
		t.Errorf("layout switches = %v, want [ru]", lays.switched)
	}
	if len(e.Log()) != 1 {
		t.Errorf("log entries = %d, want 1", len(e.Log()))
	}
}

func TestRunFallsBackToWord(t *testing.T) {
	e, _, clip, _, _ := newTestEngine(t, baseConfig(), "")
	clip.copied = "" // nothing selected anywhere

	_, err := e.Run(config.ActionSelection)
	if err == nil {
		t.Fatal("expected an error when there is nothing to convert")
	}
	if !strings.Contains(err.Error(), "нечего конвертировать") {
		t.Errorf("unexpected error: %v", err)
	}
	// Nothing was copied, so the clipboard must not be touched at all.
	if len(clip.written) != 0 {
		t.Errorf("the clipboard was written on a failed conversion: %v", clip.written)
	}
}

func TestRunWordActionSkipsSelectionProbe(t *testing.T) {
	e, kb, clip, _, _ := newTestEngine(t, baseConfig(), "vjq")

	if _, err := e.Run(config.ActionWord); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Word action must not probe the selection first.
	if clip.begins != 1 {
		t.Errorf("word action began %d copy attempts, want 1", clip.begins)
	}
	if clip.text != "мой" {
		t.Errorf("clipboard = %q, want %q", clip.text, "мой")
	}
	if len(kb.taps) == 0 {
		t.Error("expected the caret to be collapsed with Right before selecting the word")
	}
}

func TestRunDisabled(t *testing.T) {
	cfg := baseConfig()
	cfg.Enabled = false
	e, kb, _, _, _ := newTestEngine(t, cfg, "ghbdtn")

	ev, err := e.Run(config.ActionSelection)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ev.Err != "выключено" {
		t.Errorf("err = %q, want выключено", ev.Err)
	}
	if len(kb.chords) != 0 {
		t.Error("a disabled engine must not send any chords")
	}
}

func TestRunRestoresClipboardWhenAsked(t *testing.T) {
	cfg := baseConfig()
	cfg.RestoreClipboard = true
	cfg.PasteDelayMs = 10
	e, _, clip, _, _ := newTestEngine(t, cfg, "Ghbdtn")
	clip.text = "старый буфер"

	if _, err := e.Run(config.ActionSelection); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The restore is deferred; Close flushes it without waiting for the timer.
	e.Close()
	if clip.text != "старый буфер" {
		t.Errorf("clipboard = %q, want the original text back", clip.text)
	}
}

func TestRunRestoresProbeAfterFailedGrab(t *testing.T) {
	e, _, clip, _, _ := newTestEngine(t, baseConfig(), "")
	clip.text = "старый буфер"

	if _, err := e.Run(config.ActionSelection); err == nil {
		t.Fatal("expected an error when there is nothing to convert")
	}
	if clip.restored != "старый буфер" {
		t.Errorf("restore hook got %q, want the original clipboard", clip.restored)
	}
	if clip.text != "старый буфер" {
		t.Errorf("clipboard = %q, want the original text back", clip.text)
	}
}

func TestRunReportsClipboardFailure(t *testing.T) {
	cfg := baseConfig()
	e, _, clip, _, _ := newTestEngine(t, cfg, "Ghbdtn")
	clip.fail = errors.New("буфер занят")

	if _, err := e.Run(config.ActionSelection); err == nil {
		t.Fatal("expected an error when the clipboard is busy")
	}
}

func TestPreview(t *testing.T) {
	out, dir := Preview("руддщ")
	if out != "hello" || dir != "ru→en" {
		t.Errorf("Preview = %q %q", out, dir)
	}
}
