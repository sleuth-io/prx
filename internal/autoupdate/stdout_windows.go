//go:build windows

package autoupdate

import "os"

// suppressStdout returns a no-op on Windows.
func suppressStdout() func() {
	return func() {}
}

// RedirectStdoutToDevNull is a no-op on Windows and returns os.Stdout.
func RedirectStdoutToDevNull() *os.File {
	return os.Stdout
}
