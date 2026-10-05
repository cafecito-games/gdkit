//go:build unix

package lint

import (
	"os"
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
	type result struct {
		linter *Linter
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		linter, err := newLinterForProject(root, config, nil)
		finished <- result{linter: linter, err: err}
	}()

	var got result
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case got = <-finished:
	case <-timer.C:
		// A blocking read-only FIFO open is released by a writer. This keeps a
		// regression from leaking the worker goroutine after the fast failure.
		writer, unblockErr := os.OpenFile(filepath.Join(root, path), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if unblockErr == nil {
			_ = writer.Close()
			select {
			case <-finished:
			case <-time.After(time.Second):
			}
		}
		t.Fatalf("FIFO rejection blocked for more than one second (unblock: %v)", unblockErr)
	}
	linter, err := got.linter, got.err
	if err == nil || linter != nil {
		t.Fatalf("newLinterForProject() = %+v, %v", linter, err)
	}
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}
