package contextstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Writers always acquire store then anchor. Stable lock files are never removed:
// unlinking a flock inode lets a later writer acquire a different inode while an
// earlier process still owns the first. Kernel release also handles crashes.
func (s *Store) lock(ctx context.Context, anchor string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.cfg.Root, 0755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.cfg.Root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.Mkdir(".dotllm", 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := root.Lstat(".dotllm")
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, conflict("store .dotllm must be a real directory")
	}
	// Reserve a missing inode exclusively, then open an existing regular file.
	// Concurrent Root.OpenFile(O_CREATE) calls return transient ENOENT on our
	// pinned macOS/Go runtime; O_EXCL also makes symlink refusal unambiguous.
	storeLock, err := root.OpenFile(".dotllm/context.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if os.IsExist(err) {
		info, e := root.Lstat(".dotllm/context.lock")
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() {
			return nil, conflict("store lock must be a regular file")
		}
		storeLock, err = root.OpenFile(".dotllm/context.lock", os.O_RDWR|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, err
	}
	releaseStore, err := lockFile(ctx, storeLock)
	if err != nil {
		return nil, err
	}
	anchorLock, err := os.OpenFile(filepath.Join(filepath.Dir(anchor), ".dotllm.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		releaseStore()
		return nil, err
	}
	releaseAnchor, err := lockFile(ctx, anchorLock)
	if err != nil {
		releaseStore()
		return nil, err
	}
	return func() { releaseAnchor(); releaseStore() }, nil
}
func lockFile(ctx context.Context, f *os.File) (func(), error) {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err != nil {
			return nil, err
		}
		return nil, conflict("lock path is not a regular file")
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
			f.Close()
			return nil, &Error{Code: "busy", Message: "another dotllm writer holds a context lock", NextAction: "Retry after the current writer finishes.", Retryable: true}
		case <-retry.C:
		}
	}
}
