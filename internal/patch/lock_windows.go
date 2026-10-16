//go:build windows

package patch

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var ErrPatchInProgress = errors.New("another Sigil instance is already patching this installation")

type installLock struct {
	handle windows.Handle
}

func acquireInstallLock(dir string) (*installLock, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	identity := strings.ToLower(filepath.Clean(abs))
	sum := sha256.Sum256([]byte(identity))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf("Local\\SigilPatch-%x", sum[:16]))
	if err != nil {
		return nil, err
	}

	// The mutex is used only as a named marker: its existence is the lock.
	// It is deliberately not owned, because Windows ties mutex ownership to
	// an OS thread and goroutines move between threads, so ReleaseMutex
	// from Close could otherwise fail.
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return nil, ErrPatchInProgress
	}
	if err != nil {
		return nil, err
	}
	return &installLock{handle: handle}, nil
}

func (l *installLock) Close() error {
	return windows.CloseHandle(l.handle)
}
