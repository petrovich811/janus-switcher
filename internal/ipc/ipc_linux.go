//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// SocketPath is where the daemon listens.
func SocketPath() string {
	if p := os.Getenv("JANUS_SOCKET"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "janus.sock")
}

// Listen binds the unix socket, replacing a stale one.
func Listen() (net.Listener, error) {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// Only remove a stale socket: a live daemon must keep its socket, otherwise
	// a second `janus daemon` would steal the hotkey commands.
	if conn, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("демон уже запущен (сокет %s отвечает)", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return ln, nil
}

// Addr renders the address for diagnostics.
func Addr() string { return SocketPath() }

func target() (network, address, tok string, err error) {
	// A unix socket in the runtime directory is private to the user already.
	return "unix", SocketPath(), "", nil
}
