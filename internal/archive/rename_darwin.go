//go:build darwin && cgo

package archive

/*
#include <stdio.h>
#include <errno.h>
#include <stdlib.h>
// RENAME_EXCL is the kernel no-clobber operation for files AND directories.
// Parent descriptors pin already-validated directories; no path prefix is
// re-resolved by libc while crossing the filesystem mutation seam.
static int archive_rename_excl(int srcfd, const char *src, int dstfd, const char *dst) {
 if (renameatx_np(srcfd, src, dstfd, dst, RENAME_EXCL) == 0) return 0;
 return errno;
}
*/
import "C"

import (
	"os"
	"syscall"
	"unsafe"
)

const renameSupported = true
const noFollow = syscall.O_NOFOLLOW

func renameNoReplace(src *os.Root, srcName string, dst *os.Root, dstName string) error {
	source, err := src.Open(".")
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := dst.Open(".")
	if err != nil {
		return err
	}
	defer destination.Close()
	a, b := C.CString(srcName), C.CString(dstName)
	defer C.free(unsafe.Pointer(a))
	defer C.free(unsafe.Pointer(b))
	if errno := C.archive_rename_excl(C.int(source.Fd()), a, C.int(destination.Fd()), b); errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlockFile(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
