//go:build windows

package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/petrovich811/janus-switcher/internal/config"
)

// portFile keeps the port and the access token of the running daemon. Windows
// has no unix sockets in the Go standard library, and a named pipe would mean
// extra blind Win32 code, so the daemon listens on a loopback port instead.
func portFile() string { return filepath.Join(config.Dir(), "ipc.port") }

// Listen binds a loopback port, invents a token and publishes both.
func Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		ln.Close()
		return nil, err
	}
	token = hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(portFile()), 0o755); err != nil {
		ln.Close()
		return nil, err
	}
	if err := os.WriteFile(portFile(), []byte(port+" "+token+"\n"), 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// Addr renders the address for diagnostics.
func Addr() string {
	_, address, _, err := target()
	if err != nil {
		return ""
	}
	return address
}

func target() (network, address, tok string, err error) {
	data, err := os.ReadFile(portFile())
	if err != nil {
		return "", "", "", ErrNoDaemon
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return "", "", "", ErrNoDaemon
	}
	if _, err := strconv.Atoi(fields[0]); err != nil {
		return "", "", "", ErrNoDaemon
	}
	return "tcp", fmt.Sprintf("127.0.0.1:%s", fields[0]), fields[1], nil
}
