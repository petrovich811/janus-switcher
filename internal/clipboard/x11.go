//go:build linux

// Package clipboard reads and writes the X11 clipboard without cgo.
//
// Under GNOME on Wayland the compositor mirrors the Wayland clipboard into the
// X11 selections (and back), so an X11 client can read what a Wayland-native
// application copied, and a Wayland application can paste what this client puts
// on the clipboard.
package clipboard

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// ErrNoDisplay means we could not reach an X server.
var ErrNoDisplay = errors.New("cannot connect to X server")

// Debug is an optional sink for protocol traces.
var Debug io.Writer

func debugf(format string, args ...any) {
	if Debug != nil {
		fmt.Fprintf(Debug, "[clip] "+format+"\n", args...)
	}
}

// ErrTimeout is returned when the selection owner never answers.
var ErrTimeout = errors.New("timeout waiting for selection")

// ErrEmpty is returned when nobody owns the selection.
var ErrEmpty = errors.New("selection is empty")

type slot struct {
	ch chan struct{}
}

// X11 is a clipboard client living on the X server.
type X11 struct {
	conn *xgb.Conn
	root xproto.Window
	win  xproto.Window

	atomClipboard xproto.Atom
	atomPrimary   xproto.Atom
	atomUTF8      xproto.Atom
	atomText      xproto.Atom
	atomTargets   xproto.Atom
	atomIncr      xproto.Atom
	// atomProps are rotating reply properties. A late SelectionNotify from a
	// timed-out request then wakes its own waiter instead of the next request.
	atomProps   [4]xproto.Atom
	propSeq     uint32
	currentProp xproto.Atom

	// reqMu serialises selection requests: the X11 protocol replies on one
	// property, and concurrent reads would overwrite each other's answers.
	reqMu       sync.Mutex
	mu          sync.Mutex
	owner       string
	lastTarget  xproto.Atom
	lastProps   map[xproto.Atom]xproto.Atom
	selWaiters  map[xproto.Atom][]*slot
	propWaiters map[xproto.Atom][]*slot
}

// Open connects to the X server named by $DISPLAY.
func Open() (*X11, error) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoDisplay, err)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	c := &X11{
		conn:        conn,
		root:        screen.Root,
		lastProps:   map[xproto.Atom]xproto.Atom{},
		selWaiters:  map[xproto.Atom][]*slot{},
		propWaiters: map[xproto.Atom][]*slot{},
	}
	intern := func(name string) xproto.Atom {
		r, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			return 0
		}
		return r.Atom
	}
	c.atomClipboard = intern("CLIPBOARD")
	c.atomPrimary = intern("PRIMARY")
	c.atomUTF8 = intern("UTF8_STRING")
	c.atomText = intern("TEXT")
	c.atomTargets = intern("TARGETS")
	c.atomIncr = intern("INCR")
	for i, name := range []string{"JANUS_SELECTION_A", "JANUS_SELECTION_B", "JANUS_SELECTION_C", "JANUS_SELECTION_D"} {
		c.atomProps[i] = intern(name)
	}

	wid, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := xproto.CreateWindowChecked(conn, screen.RootDepth, wid, c.root,
		0, 0, 1, 1, 0, xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		conn.Close()
		return nil, err
	}
	c.win = wid
	go c.loop()
	return c, nil
}

func (c *X11) loop() {
	for {
		ev, err := c.conn.WaitForEvent()
		if err != nil || ev == nil {
			if err != nil {
				debugf("event loop error: %v", err)
			}
			return
		}
		switch e := ev.(type) {
		case xproto.SelectionNotifyEvent:
			debugf("SelectionNotify target=%d property=%d", e.Target, e.Property)
			// A success carries the property of its request. A refusal carries
			// Property=0; attribute it to the request that is currently active.
			c.mu.Lock()
			c.lastTarget = e.Target
			if e.Property != 0 {
				c.lastProps[e.Property] = e.Property
				c.mu.Unlock()
				c.wake(c.selWaiters, e.Property)
				break
			}
			p := c.currentProp
			c.lastProps[p] = 0
			c.mu.Unlock()
			c.wake(c.selWaiters, p)
		case xproto.PropertyNotifyEvent:
			debugf("PropertyNotify atom=%d state=%d", e.Atom, e.State)
			if e.State == xproto.PropertyNewValue {
				c.wake(c.propWaiters, e.Atom)
			}
		case xproto.SelectionRequestEvent:
			debugf("SelectionRequest target=%d property=%d text=%q", e.Target, e.Property, c.owned())
			c.handleSelectionRequest(e)
		case xproto.SelectionClearEvent:
			c.mu.Lock()
			c.owner = ""
			c.mu.Unlock()
		}
	}
}

func (c *X11) wake(m map[xproto.Atom][]*slot, atom xproto.Atom) {
	c.mu.Lock()
	ws := m[atom]
	delete(m, atom)
	c.mu.Unlock()
	for _, s := range ws {
		close(s.ch)
	}
}

func (c *X11) add(m map[xproto.Atom][]*slot, atom xproto.Atom) *slot {
	s := &slot{ch: make(chan struct{})}
	c.mu.Lock()
	m[atom] = append(m[atom], s)
	c.mu.Unlock()
	return s
}

func (c *X11) drop(m map[xproto.Atom][]*slot, atom xproto.Atom, s *slot) {
	c.mu.Lock()
	ws := m[atom]
	for i, x := range ws {
		if x == s {
			m[atom] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	c.mu.Unlock()
}

func (c *X11) await(m map[xproto.Atom][]*slot, atom xproto.Atom, s *slot, timeout time.Duration) bool {
	select {
	case <-s.ch:
		return true
	case <-time.After(timeout):
		c.drop(m, atom, s)
		return false
	}
}

func (c *X11) handleSelectionRequest(e xproto.SelectionRequestEvent) {
	reply := xproto.SelectionNotifyEvent{
		Time: e.Time, Requestor: e.Requestor, Selection: e.Selection,
		Target: e.Target, Property: e.Property,
	}
	if e.Property == 0 {
		reply.Property = e.Target
	}
	text := c.owned()

	ok := true
	switch {
	case e.Target == c.atomTargets:
		atoms := []uint32{uint32(c.atomUTF8), uint32(c.atomText), uint32(xproto.AtomString), uint32(c.atomTargets)}
		buf := make([]byte, len(atoms)*4)
		for i, a := range atoms {
			buf[i*4] = byte(a)
			buf[i*4+1] = byte(a >> 8)
			buf[i*4+2] = byte(a >> 16)
			buf[i*4+3] = byte(a >> 24)
		}
		if err := xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, e.Requestor,
			reply.Property, xproto.AtomAtom, 32, uint32(len(atoms)), buf).Check(); err != nil {
			ok = false
		}
	case e.Target == c.atomUTF8 || e.Target == c.atomText || e.Target == xproto.AtomString:
		if err := xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, e.Requestor,
			reply.Property, e.Target, 8, uint32(len(text)), []byte(text)).Check(); err != nil {
			ok = false
		}
	default:
		ok = false
	}
	if !ok {
		reply.Property = 0
	}
	if err := xproto.SendEventChecked(c.conn, false, e.Requestor, 0, string(reply.Bytes())).Check(); err != nil {
		debugf("SendEvent failed: %v", err)
	}
}

// ReadSelection fetches the contents of a selection as text.
func (c *X11) ReadSelection(selection xproto.Atom, timeout time.Duration) (string, error) {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	owner, err := xproto.GetSelectionOwner(c.conn, selection).Reply()
	if err != nil {
		return "", err
	}
	if owner.Owner == 0 {
		return "", ErrEmpty
	}
	// Pick a fresh reply property for this request. It is safe because reqMu
	// keeps only one request in flight at a time.
	replyProp := c.atomProps[c.propSeq%uint32(len(c.atomProps))]
	c.propSeq++
	c.mu.Lock()
	c.currentProp = replyProp
	delete(c.lastProps, replyProp)
	c.mu.Unlock()
	s := c.add(c.selWaiters, replyProp)
	if err := xproto.ConvertSelectionChecked(c.conn, c.win, selection, c.atomUTF8,
		replyProp, 0).Check(); err != nil {
		c.drop(c.selWaiters, replyProp, s)
		return "", err
	}
	if !c.await(c.selWaiters, replyProp, s, timeout) {
		return "", ErrTimeout
	}
	c.mu.Lock()
	answer := c.lastProps[replyProp]
	c.mu.Unlock()
	if answer == 0 {
		// The owner refused the conversion.
		return "", ErrEmpty
	}
	prop, err := xproto.GetProperty(c.conn, true, c.win, replyProp, 0, 0, 1024*1024).Reply()
	if err != nil {
		return "", err
	}
	if prop.Type == c.atomIncr {
		return c.readIncrement(replyProp)
	}
	if prop.ValueLen == 0 {
		return "", ErrEmpty
	}
	return string(prop.Value), nil
}

// readIncrement continues an INCR transfer.
func (c *X11) readIncrement(prop xproto.Atom) (string, error) {
	var out []byte
	for i := 0; i < 100; i++ {
		s := c.add(c.propWaiters, prop)
		chunk, err := xproto.GetProperty(c.conn, true, c.win, prop, 0, 0, 1024*1024).Reply()
		if err != nil {
			c.drop(c.propWaiters, prop, s)
			return "", err
		}
		if chunk.ValueLen == 0 {
			c.drop(c.propWaiters, prop, s)
			break
		}
		out = append(out, chunk.Value...)
		if !c.await(c.propWaiters, prop, s, 2*time.Second) {
			break
		}
	}
	return string(out), nil
}

// ReadText returns the CLIPBOARD text.
func (c *X11) ReadText() (string, error) {
	return c.ReadSelection(c.atomClipboard, 1500*time.Millisecond)
}

// ReadPrimary returns the PRIMARY selection text.
func (c *X11) ReadPrimary() (string, error) {
	return c.ReadSelection(c.atomPrimary, 800*time.Millisecond)
}

// probe is a marker put on the clipboard so that a copy can be detected even
// when the application copies exactly the text that was there before.
const probe = "janus-clipboard-probe-9d3f"

// RestoreAfterFailedCopy replaces our probe with the text the user had before.
// If the probe is already gone (a copy happened), it leaves the clipboard alone.
func (c *X11) RestoreAfterFailedCopy(original string) {
	if c.owned() != probe {
		return
	}
	if err := c.WriteText(original); err != nil {
		debugf("restore after failed copy: %v", err)
	}
}

// BeginCopy puts the probe on the clipboard and returns it as the token.
func (c *X11) BeginCopy() string {
	if err := c.WriteText(probe); err != nil {
		debugf("probe write failed: %v", err)
	}
	return probe
}

// WaitCopied returns the clipboard text once it stops being the probe.
func (c *X11) WaitCopied(token string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t, err := c.ReadText(); err == nil && t != "" && t != token {
			return t, true
		}
		time.Sleep(30 * time.Millisecond)
	}
	return "", false
}

// WriteText takes ownership of CLIPBOARD and serves text until it is replaced.
func (c *X11) WriteText(text string) error {
	c.mu.Lock()
	c.owner = text
	c.mu.Unlock()
	if err := xproto.SetSelectionOwnerChecked(c.conn, c.win, c.atomClipboard, 0).Check(); err != nil {
		return err
	}
	return nil
}

func (c *X11) owned() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.owner
}

// OwnedText reports the text we are currently serving.
func (c *X11) OwnedText() string { return c.owned() }

// Close releases the X connection.
func (c *X11) Close() { c.conn.Close() }
