package store

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// renameat2 numbers (the syscall package only names it on some
// architectures).
var sysRenameat2 = map[string]uintptr{"amd64": 316, "arm64": 276, "arm": 382, "386": 353, "riscv64": 276}

const renameNoReplace = 1 // RENAME_NOREPLACE

// renameNoReplaceAt renames srcName in srcDir to dstName in dstDir, failing
// with EEXIST when dstName exists, in one system call (renameat2 with
// RENAME_NOREPLACE). errNoReplaceUnsupported when the kernel or the file
// system cannot do it (old kernels, some exFAT drivers).
func renameNoReplaceAt(srcDir *os.File, srcName string, dstDir *os.File, dstName string) error {
	nr, ok := sysRenameat2[runtime.GOARCH]
	if !ok {
		return errNoReplaceUnsupported
	}
	src, err := syscall.BytePtrFromString(srcName)
	if err != nil {
		return err
	}
	dst, err := syscall.BytePtrFromString(dstName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(nr, srcDir.Fd(), uintptr(unsafe.Pointer(src)), dstDir.Fd(), uintptr(unsafe.Pointer(dst)), renameNoReplace, 0)
	runtime.KeepAlive(srcDir)
	runtime.KeepAlive(dstDir)
	switch errno {
	case 0:
		return nil
	case syscall.ENOSYS, syscall.EINVAL, syscall.ENOTSUP:
		return errNoReplaceUnsupported
	default:
		return errno
	}
}
