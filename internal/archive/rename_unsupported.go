//go:build (!darwin && !linux) || (darwin && !cgo) || (linux && !amd64 && !arm64)

package archive

import "os"

const renameSupported = false
const noFollow = 0

func renameNoReplace(*os.Root, string, *os.Root, string) error {
	return problem("unavailable", "atomic no-replace rename is unsupported on this platform")
}
func lockFile(*os.File) error {
	return problem("unavailable", "archive locking is unsupported on this platform")
}
func unlockFile(*os.File) {}
