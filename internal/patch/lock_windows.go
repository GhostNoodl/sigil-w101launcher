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

	handle, err := windows.CreateMutex(nil, true, name)
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
	releaseErr := windows.ReleaseMutex(l.handle)
	closeErr := windows.CloseHandle(l.handle)
	return errors.Join(releaseErr, closeErr)
}
