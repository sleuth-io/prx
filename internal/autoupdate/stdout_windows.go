//go:build windows

package autoupdate

// suppressStdout returns a no-op on Windows.
func suppressStdout() func() {
	return func() {}
}
