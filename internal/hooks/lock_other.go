//go:build !unix && !windows

package hooks

import "os"

func lockFile(*os.File) error { return nil }
