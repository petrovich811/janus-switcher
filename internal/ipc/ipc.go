// Package ipc is the tiny protocol between the CLI (and the hotkey command on
// Linux) and the running daemon. The transport differs per platform: a unix
// socket on Linux, a loopback TCP port on Windows.
package ipc

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ErrNoDaemon means nothing is listening.
var ErrNoDaemon = errors.New("daemon is not running")

// token guards the transport where file permissions cannot: a loopback TCP
// port on Windows is reachable by any local process (and by a web page), so
// every command must carry the token the daemon wrote next to its port.
var token string

// Serve accepts connections and answers every line with handler.
func Serve(ln net.Listener, handler func(cmd string) string) func() {
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(20 * time.Second))
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil && line == "" {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if token != "" {
					if !strings.HasPrefix(line, token+" ") {
						_, _ = c.Write([]byte("err: unauthorized\n"))
						return
					}
					line = strings.TrimPrefix(line, token+" ")
				}
				ans := handler(line)
				_, _ = c.Write([]byte(ans + "\n"))
			}(conn)
		}
	}()
	return func() { _ = ln.Close() }
}

// Send delivers one command and returns the answer.
func Send(cmd string) (string, error) {
	network, address, tok, err := target()
	if err != nil {
		return "", err
	}
	conn, err := net.DialTimeout(network, address, 2*time.Second)
	if err != nil {
		return "", ErrNoDaemon
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	line := cmd
	if tok != "" {
		line = tok + " " + cmd
	}
	if _, err := fmt.Fprintf(conn, "%s\n", line); err != nil {
		return "", err
	}
	answer, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && answer == "" {
		return "", err
	}
	return strings.TrimRight(answer, "\r\n"), nil
}
