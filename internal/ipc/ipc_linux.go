//go:build linux

package ipc

import (
	"net"
	"os"
	"path/filepath"
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
