//go:build windows

package main

import (
	"os"
	"syscall"
)

// attachParentConsole connects to the invoking terminal's console so CLI
// output stays visible even though the binary is built with the windowsgui
// subsystem (which is what suppresses the console window on double-click).
// No-op when there is no parent console (e.g. launched from Explorer).
func attachParentConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	const attachParentProcess = 0xFFFFFFFF // (DWORD)-1
	r, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess)
	if r == 0 {
		return
	}

	getStdHandle := kernel32.NewProc("GetStdHandle")
	reopen := func(stdHandle uintptr, name string) *os.File {
		h, _, _ := getStdHandle.Call(stdHandle)
		if h == 0 {
			return nil
		}
		return os.NewFile(h, name)
	}

	// STD_INPUT_HANDLE (DWORD)-10, STD_OUTPUT_HANDLE (DWORD)-11,
	// STD_ERROR_HANDLE (DWORD)-12, as uintptr two's complement.
	if f := reopen(0xFFFFFFF6, "stdin"); f != nil {
		os.Stdin = f
	}
	if f := reopen(0xFFFFFFF5, "stdout"); f != nil {
		os.Stdout = f
	}
	if f := reopen(0xFFFFFFF4, "stderr"); f != nil {
		os.Stderr = f
	}
}
