//go:build !windows

package autoupdate

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// suppressStdout redirects stdout to /dev/null at the file descriptor level.
// Returns a function to restore stdout. This is needed because the go-selfupdate
// library writes directly to file descriptor 1, bypassing os.Stdout.
func suppressStdout() func() {
	origStdout, err := syscall.Dup(syscall.Stdout)
	if err != nil {
		return func() {}
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = syscall.Close(origStdout)
		return func() {}
	}

	if err := unix.Dup2(int(devNull.Fd()), syscall.Stdout); err != nil {
		_ = devNull.Close()
		_ = syscall.Close(origStdout)
		return func() {}
	}

	_ = devNull.Close()

	return func() {
		_ = unix.Dup2(origStdout, syscall.Stdout)
		_ = syscall.Close(origStdout)
	}
}

// RedirectStdoutToDevNull dups the current stdout into a new *os.File and
// points file descriptor 1 at /dev/null. The returned file is a handle to the
// real terminal for callers (e.g. Bubble Tea) that need to keep writing to it;
// anything written directly to FD 1 afterward (including by third-party
// libraries like go-selfupdate) is discarded.
//
// Callers should pass the returned file to tea.WithOutput so the TUI's
// terminal-control sequences reach the real terminal even while the
// autoupdater is running.
func RedirectStdoutToDevNull() *os.File {
	origFD, err := syscall.Dup(syscall.Stdout)
	if err != nil {
		return os.Stdout
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = syscall.Close(origFD)
		return os.Stdout
	}

	if err := unix.Dup2(int(devNull.Fd()), syscall.Stdout); err != nil {
		_ = devNull.Close()
		_ = syscall.Close(origFD)
		return os.Stdout
	}
	_ = devNull.Close()

	return os.NewFile(uintptr(origFD), "/dev/stdout")
}
