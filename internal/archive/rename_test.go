//go:build (darwin && cgo) || (linux && (amd64 || arm64))

package archive

import (
	"os"
	"path/filepath"
	"testing"
)

// The platform adapter is an internal seam: exercise the actual kernel
// operation against occupied files, empty directories and dangling symlinks.
// A check-then-os.Rename implementation would overwrite these destinations.
func TestPlatformRenameNeverReplacesOccupiedDestination(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "dangling-symlink"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			src, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			source := filepath.Join(base, "source")
			destination := filepath.Join(base, "destination")
			if kind == "empty-directory" {
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(source, []byte("source bytes"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "file" {
					if err := os.WriteFile(destination, []byte("destination bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink("missing", destination); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(destination)
			if err != nil {
				t.Fatal(err)
			}
			if err := renameNoReplace(src, "source", src, "destination"); err == nil {
				t.Fatal("no-replace rename overwrote destination")
			}
			after, err := os.Lstat(destination)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("destination replaced: %v", err)
			}
			if _, err := os.Lstat(source); err != nil {
				t.Fatalf("source lost: %v", err)
			}
			if kind == "file" {
				data, err := os.ReadFile(destination)
				if err != nil || string(data) != "destination bytes" {
					t.Fatalf("destination bytes changed: %q %v", data, err)
				}
			}
		})
	}
}
