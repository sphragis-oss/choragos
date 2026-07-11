// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// absFileArg validates a readable, non-empty regular file and returns its absolute
// path, so the deck and workers can resolve it regardless of their working directory.
func absFileArg(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return "", fmt.Errorf("%s: must be a non-empty regular file", abs)
	}
	return abs, nil
}
