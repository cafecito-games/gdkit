//go:build unix

package lint

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cafecito-games/gdkit/internal/failure"
)

func TestExtensionAPIRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	if err := syscall.Mkfifo(filepath.Join(root, path), 0o600); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.ExtensionAPI = &path
	started := time.Now()
	linter, err := newLinterForProject(root, config, nil)
	if err == nil || linter != nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO rejection blocked for %v", elapsed)
	}
	assertFailure(t, err, failure.ConfigInvalid, path)
}
