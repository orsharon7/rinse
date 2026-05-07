// Package demo provides small helpers used by the rinse review-loop demo.
package demo

import (
	"os"
)

// WriteConfig writes config bytes to the given path.
func WriteConfig(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// FirstByte returns the first byte of data and whether data was non-empty.
func FirstByte(data []byte) (byte, bool) {
	if len(data) == 0 {
		return 0, false
	}
	return data[0], true
}
