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
