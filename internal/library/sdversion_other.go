//go:build !unix

package library

import "io/fs"

// sdVersion: no inode here; the size and the date are compared alone.
func sdVersion(fs.FileInfo) string { return "" }
