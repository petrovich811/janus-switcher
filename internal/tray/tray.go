// Package tray draws a StatusNotifierItem icon in the desktop panel and its
// DBus menu. It speaks the protocol directly, so no cgo or GTK is needed.
package tray

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	itemPath   = dbus.ObjectPath("/StatusNotifierItem")
	menuPath   = dbus.ObjectPath("/MenuBar")
	itemIface  = "org.kde.StatusNotifierItem"
	menuIface  = "com.canonical.dbusmenu"
	watcherDst = "org.kde.StatusNotifierWatcher"
	watcherObj = dbus.ObjectPath("/StatusNotifierWatcher")
)

// Callbacks are the actions the menu can trigger.
type Callbacks struct {
	OpenUI  func()
	Toggle  func() bool // returns the new enabled state
	Quit    func()
	Enabled func() bool
	Title   func() string
	Logf    func(format string, args ...any)
}

// Item is a live tray icon.
type Item struct {
	conn  *dbus.Conn
	cb    Callbacks
	props *prop.Properties
	mmenu *prop.Properties

	mu         sync.Mutex
	revision   uint32
	name       string
	registered bool
}

// Available reports whether a StatusNotifierWatcher is present, which means the
// desktop can show tray icons at all.
func Available(conn *dbus.Conn) bool {
	obj := conn.Object("org.freedesktop.DBus", dbus.ObjectPath("/org/freedesktop/DBus"))
	var names []string
	if err := obj.Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false
	}
	for _, n := range names {
		if n == watcherDst {
			return true
		}
	}
	return false
}

// RegisteredCount counts the StatusNotifierItem icons already on the bus, so a
// second process can tell whether a running daemon holds the tray icon.
func RegisteredCount(conn *dbus.Conn) int {
	obj := conn.Object("org.freedesktop.DBus", dbus.ObjectPath("/org/freedesktop/DBus"))
	var names []string
	if err := obj.Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return 0
	}
	var n int
	for _, name := range names {
		if strings.HasPrefix(name, "org.kde.StatusNotifierItem-") {
			n++
		}
	}
	return n
}

// Add registers the tray icon on the session bus.
func Add(conn *dbus.Conn, cb Callbacks) (*Item, error) {
	it := &Item{conn: conn, cb: cb, revision: 1}

	// --- the icon itself -------------------------------------------------
	status := "Active"
	iconProps, err := prop.Export(conn, itemPath, map[string]map[string]*prop.Prop{
		itemIface: {
			"Id":         {Value: "janus", Writable: false, Emit: prop.EmitFalse},
			"Category":   {Value: "ApplicationStatus", Writable: false, Emit: prop.EmitFalse},
			"Status":     {Value: status, Writable: false, Emit: prop.EmitTrue},
			"Title":      {Value: "Janus", Writable: false, Emit: prop.EmitTrue},
			"IconName":   {Value: "input-keyboard", Writable: false, Emit: prop.EmitTrue},
			"IconPixmap": {Value: Icons(), Writable: false, Emit: prop.EmitTrue},
			"Menu":       {Value: menuPath, Writable: false, Emit: prop.EmitFalse},
			"ItemIsMenu": {Value: true, Writable: false, Emit: prop.EmitFalse},
			"ToolTip":    {Value: [3]interface{}{"input-keyboard", Icons(), "Janus"}, Writable: false, Emit: prop.EmitTrue},
			// Optional properties, but the Ubuntu panel asks for them and
			// throws when one it expects is missing.
			"AttentionIconName":   {Value: "", Writable: false, Emit: prop.EmitFalse},
			"AttentionIconPixmap": {Value: []Pixmap{}, Writable: false, Emit: prop.EmitFalse},
			"OverlayIconName":     {Value: "", Writable: false, Emit: prop.EmitFalse},
			"OverlayIconPixmap":   {Value: []Pixmap{}, Writable: false, Emit: prop.EmitFalse},
			"WindowId":            {Value: uint32(0), Writable: false, Emit: prop.EmitFalse},
			"IconThemePath":       {Value: "", Writable: false, Emit: prop.EmitFalse},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("export icon properties: %w", err)
	}
	it.props = iconProps

	// --- the menu --------------------------------------------------------
	menuProps, err := prop.Export(conn, menuPath, map[string]map[string]*prop.Prop{
		menuIface: {
			"Version":       {Value: uint32(3), Writable: false, Emit: prop.EmitFalse},
			"Status":        {Value: "normal", Writable: false, Emit: prop.EmitFalse},
			"TextDirection": {Value: "ltr", Writable: false, Emit: prop.EmitFalse},
			"IconThemePath": {Value: []string{}, Writable: false, Emit: prop.EmitFalse},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("export menu properties: %w", err)
	}
	it.mmenu = menuProps

	if err := conn.Export(it, itemPath, itemIface); err != nil {
		return nil, err
	}
	if err := conn.Export(it, menuPath, menuIface); err != nil {
		return nil, err
	}
	// Introspection is optional for panels but makes the interface debuggable.
	for _, n := range []*introspect.Node{
		{Name: string(itemPath), Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{Name: itemIface, Methods: introspect.Methods(it)},
		}},
		{Name: string(menuPath), Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{Name: menuIface, Methods: introspect.Methods(it)},
		}},
	} {
		if err := conn.Export(introspect.NewIntrospectable(n), dbus.ObjectPath(n.Name),
			"org.freedesktop.DBus.Introspectable"); err != nil {
			return nil, err
		}
	}

	// --- the bus name and the watcher ------------------------------------
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		name = fmt.Sprintf("org.kde.StatusNotifierItem-%d-2", os.Getpid())
		if _, err := conn.RequestName(name, dbus.NameFlagDoNotQueue); err != nil {
			return nil, fmt.Errorf("request tray bus name: %w", err)
		}
	}
	it.name = name

	if !Available(conn) {
		// The watcher often appears a second or two after the session starts.
		// Keep the item and let Watch register it later; returning an error
		// here would disable the tray until a manual restart.
		it.logf("tray: StatusNotifierWatcher is not on the bus yet; will retry")
		return it, nil
	}
	w := conn.Object(watcherDst, watcherObj)
	if err := w.Call(watcherDst+".RegisterStatusNotifierItem", 0, name).Err; err != nil {
		it.logf("tray: register with watcher: %v; will retry", err)
		return it, nil
	}
	it.setRegistered(true)
	return it, nil
}

func (it *Item) logf(format string, args ...any) {
	if it.cb.Logf != nil {
		it.cb.Logf(format, args...)
	}
}

func (it *Item) setRegistered(v bool) bool {
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.registered == v {
		return false
	}
	it.registered = v
	return true
}

// Close gives the bus name back so that the panel drops the icon at once.
func (it *Item) Close() {
	it.setRegistered(false)
	if it.name != "" {
		_, _ = it.conn.ReleaseName(it.name)
	}
}

// Watch keeps the icon registered. The Ubuntu indicator extension is fragile:
// it unloads itself when any client's D-Bus object disappears mid-check, and
// then no tray icons exist at all until the next login. This goroutine notices
// a watcher coming back and registers again.
func (it *Item) Watch(done <-chan struct{}) {
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if !Available(it.conn) {
					if it.setRegistered(false) {
						it.logf("tray: StatusNotifierWatcher disappeared")
					}
					continue
				}
				err := it.conn.Object(watcherDst, watcherObj).
					Call(watcherDst+".RegisterStatusNotifierItem", 0, it.name).Err
				if it.setRegistered(err == nil) {
					if err != nil {
						it.logf("tray: registration failed: %v", err)
					} else {
						it.logf("tray: registered with StatusNotifierWatcher")
					}
				}
			}
		}
	}()
}

// Registered reports whether our icon is currently registered with the watcher.
func (it *Item) Registered() bool {
	it.mu.Lock()
	defer it.mu.Unlock()
	return it.registered
}

// Refresh updates the tooltip and the menu after the configuration changed.
func (it *Item) Refresh() {
	it.mu.Lock()
	it.revision++
	rev := it.revision
	it.mu.Unlock()
	if it.props != nil {
		it.props.SetMust(itemIface, "Title", it.title())
		it.props.SetMust(itemIface, "ToolTip", [3]interface{}{"input-keyboard", Icons(), it.title()})
		it.props.SetMust(itemIface, "Status", "Active")
	}
	_ = it.conn.Emit(menuPath, menuIface+".LayoutUpdated", rev, int32(0))
}

func (it *Item) title() string {
	if it.cb.Title != nil {
		return it.cb.Title()
	}
	return "Janus"
}

// --- org.kde.StatusNotifierItem methods ----------------------------------

// Activate is a left click: open the settings page.
func (it *Item) Activate(x, y int32) *dbus.Error {
	if it.cb.OpenUI != nil {
		it.cb.OpenUI()
	}
	return nil
}

// SecondaryActivate is a middle click: toggle the switcher.
func (it *Item) SecondaryActivate(x, y int32) *dbus.Error {
	if it.cb.Toggle != nil {
		it.cb.Toggle()
		it.Refresh()
	}
	return nil
}

// ContextMenu is a right click: the menu is served over DBus, nothing to do.
func (it *Item) ContextMenu(x, y int32) *dbus.Error { return nil }

// Scroll is a wheel click; unused but part of the interface.
func (it *Item) Scroll(delta int32, orientation string) *dbus.Error { return nil }

// --- com.canonical.dbusmenu ---------------------------------------------

// MenuItem is the wire form of a DBus menu node.
type MenuItem struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}

func label(s string) dbus.Variant { return dbus.MakeVariant(s) }

// build creates the menu tree.
func (it *Item) build(parent int32) MenuItem {
	switch parent {
	case 0:
		enabled := true
		if it.cb.Enabled != nil {
			enabled = it.cb.Enabled()
		}
		state := int32(0)
		text := "Включить"
		if enabled {
			state = 1
			text = "Включён"
		}
		root := MenuItem{ID: 0, Properties: map[string]dbus.Variant{
			"children-display": label("submenu"),
		}}
		add := func(mi MenuItem) {
			root.Children = append(root.Children, dbus.MakeVariant(mi))
		}
		add(MenuItem{ID: 1, Properties: map[string]dbus.Variant{
			"label": label("Открыть настройки"), "enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true),
		}})
		add(MenuItem{ID: 2, Properties: map[string]dbus.Variant{
			"label": label(text), "enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true), "toggle-type": label("checkmark"),
			"toggle-state": dbus.MakeVariant(state),
		}})
		add(MenuItem{ID: 3, Properties: map[string]dbus.Variant{
			"type": label("separator"), "visible": dbus.MakeVariant(true),
		}})
		add(MenuItem{ID: 4, Properties: map[string]dbus.Variant{
			"label": label("Выход"), "enabled": dbus.MakeVariant(true),
			"visible": dbus.MakeVariant(true),
		}})
		return root
	}
	return MenuItem{ID: parent, Properties: map[string]dbus.Variant{}}
}

// GetLayout returns the menu tree.
func (it *Item) GetLayout(parent int32, depth int32, props []string) (uint32, MenuItem, *dbus.Error) {
	it.mu.Lock()
	rev := it.revision
	it.mu.Unlock()
	return rev, it.build(parent), nil
}

// PropEntry is one row of GetGroupProperties.
type PropEntry struct {
	ID    int32
	Props map[string]dbus.Variant
}

// GetGroupProperties returns properties for several items at once.
func (it *Item) GetGroupProperties(ids []int32, props []string) ([]PropEntry, *dbus.Error) {
	root := it.build(0)
	all := []MenuItem{root}
	for _, ch := range root.Children {
		var mi MenuItem
		if err := ch.Store(&mi); err == nil {
			all = append(all, mi)
		}
	}
	var out []PropEntry
	want := map[int32]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, mi := range all {
		if len(want) == 0 || want[mi.ID] {
			out = append(out, PropEntry{ID: mi.ID, Props: mi.Properties})
		}
	}
	return out, nil
}

// Event handles a click on a menu item.
func (it *Item) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	if eventID != "clicked" {
		return nil
	}
	switch id {
	case 1:
		if it.cb.OpenUI != nil {
			it.cb.OpenUI()
		}
	case 2:
		if it.cb.Toggle != nil {
			it.cb.Toggle()
			it.Refresh()
		}
	case 4:
		if it.cb.Quit != nil {
			it.cb.Quit()
		}
	}
	return nil
}

// AboutToShow is a hint that the menu is opening.
func (it *Item) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

// AboutToShowGroup answers the batched variant of AboutToShow.
func (it *Item) AboutToShowGroup(ids []int32) ([]int32, []int32, *dbus.Error) {
	return nil, nil, nil
}
