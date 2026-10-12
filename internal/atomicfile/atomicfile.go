// Package atomicfile replaces files so that readers and crashes never see a
// partial file.
package atomicfile

import (
	"os"
	"path/filepath"
)

// WriteFile replaces path with data, with permissions perm, so that a crash
// leaves either the old or the new content, never a partial file. It writes
// to a uniquely named temporary file next to path and renames it into place.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // no-op after a successful rename
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { // persist the rename where supported
		_ = d.Sync()
		d.Close()
	}
	return nil
}
