package patch

import (
	"io"
	"os"
	"path/filepath"
)

// writeAtomic writes filename through a temporary file in the same directory.
// Rename is therefore atomic and never exposes a partial cache or CRC file.
func writeAtomic(filename string, write func(io.Writer) error) (err error) {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".sigil-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = write(tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	tmp = nil
	if err = os.Rename(tmpName, filename); err != nil {
		return err
	}
	return nil
}
