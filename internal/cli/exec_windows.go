//go:build windows

package cli

import "os"

// execReplace on Windows falls back to execInherited because syscall.Exec is
// not available on this platform. RINSE targets Linux and macOS; Windows is
// unsupported but the package must compile cleanly for `go test ./...`.
func execReplace(args []string) {
	os.Exit(execInherited(args))
}
