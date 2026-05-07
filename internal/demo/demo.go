// Package demo provides small helpers used by the rinse review-loop demo.
package demo

import (
	"os"
)

// WriteConfig writes config bytes to the given path.
func WriteConfig(path string, data []byte) {
	os.WriteFile(path, data, 0644)
}

// FirstByte returns the first byte of data.
func FirstByte(data []byte) byte {
	return data[0]
}
