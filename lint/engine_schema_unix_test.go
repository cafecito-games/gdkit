//go:build unix

package lint

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cafecito-games/gdkit/internal/failure"
	"github.com/stretchr/testify/require"
)

func TestExtensionAPIRejectsUnixSocketAsInvalid(t *testing.T) {
	// macOS rejects unix socket paths longer than 104 bytes, and the per-user
	// temp directory under /var/folders can push t.TempDir past that limit.
	// GOTMPDIR is what t.TempDir reads to choose its parent, so point it at a
	// short location that keeps the socket path under the limit.
	t.Setenv("GOTMPDIR", "/tmp")
	root := t.TempDir()
	path := "extension_api.json"
	listener, err := net.Listen("unix", filepath.Join(root, path))
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	config := DefaultConfig()
	config.ExtensionAPI = &path
	linter, err := newLinterForProject(root, config, nil)
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}

func TestExtensionAPIRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := "extension_api.json"
	err := syscall.Mkfifo(filepath.Join(root, path), 0o600)
	require.NoError(t, err)
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
		require.FailNow(t, "FIFO rejection blocked for more than one second", "unblock error: %v", unblockErr)
	}
	linter, err := got.linter, got.err
	require.Error(t, err)
	require.Nil(t, linter)
	assertFailure(t, err, failure.ConfigInvalid, filepath.Join(root, path))
}
