//go:build linux && (amd64 || arm64)

package archive

import (
	"os"
	"syscall"
	"unsafe"
)

const renameSupported = true
const noFollow = syscall.O_NOFOLLOW

// Linux's private adapter matches the macOS descriptor-relative no-clobber
// contract. Unsupported filesystems return the kernel error without a fallback.
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
	a, err := syscall.BytePtrFromString(srcName)
	if err != nil {
		return err
	}
	b, err := syscall.BytePtrFromString(dstName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(renameat2Trap, source.Fd(), uintptr(unsafe.Pointer(a)), destination.Fd(), uintptr(unsafe.Pointer(b)), 1, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlockFile(f *os.File)     { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
