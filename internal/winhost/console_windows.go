//go:build windows

package winhost

import (
	"os"

	"golang.org/x/sys/windows"
)

var procAttachConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")

// EnsureConsole makes CLI output visible when the GUI binary is started from
// cmd.exe or PowerShell. Started from Explorer it simply does nothing.
func EnsureConsole() {
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
}
