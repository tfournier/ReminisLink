//go:build !unix

package store

import "io/fs"

// identityOf: no inode here.
func identityOf(fs.FileInfo) string { return "" }
