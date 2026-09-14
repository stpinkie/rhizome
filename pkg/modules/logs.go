// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"os"
	"strings"
)

// tailFile returns the last n lines of a file. Reads at most 1 MiB from the
// tail so multi-GB logs are cheap to inspect.
func tailFile(path string, n int) (string, error) {
	const maxRead = 1 << 20
	//nolint:gosec // G304: path is a module log file under the modules root.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := int64(0)
	if st.Size() > maxRead {
		start = st.Size() - maxRead
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) > 0 {
		// ReadAt returns io.EOF at exact-end reads; partial data is still fine.
		if _, ok := err.(*os.PathError); ok {
			return "", err
		}
	}

	text := string(buf)
	if start > 0 {
		// Drop the first partial line.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}
