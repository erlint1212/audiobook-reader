// Package atomicfile replaces files via temp file + rename, so a crash never
// leaves a torn file behind.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Write puts data at dir/name. The temp file lives in dir, so the rename
// stays on one filesystem and is atomic.
func Write(dir, name string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: %w", err)
	}
	renamed := false
	defer func() {
		if renamed {
			return
		}
		if rmErr := os.Remove(tmp.Name()); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("atomicfile: %w", rmErr))
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return errors.Join(fmt.Errorf("atomicfile: %w", err), tmp.Close())
	}
	if err := tmp.Sync(); err != nil {
		return errors.Join(fmt.Errorf("atomicfile: %w", err), tmp.Close())
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("atomicfile: %w", err)
	}
	renamed = true
	return nil
}
