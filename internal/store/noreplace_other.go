//go:build !linux

package store

import "os"

// renameNoReplaceAt has no portable system call off Linux (macOS has
// renameatx_np, not reachable without cgo): the callers fall back to an
// exclusive link (files) or an exclusive folder (folders).
func renameNoReplaceAt(*os.File, string, *os.File, string) error { return errNoReplaceUnsupported }
