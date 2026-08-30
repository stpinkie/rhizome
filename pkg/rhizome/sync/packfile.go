package sync

import (
	"bytes"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
	"github.com/go-git/go-git/v5/plumbing/revlist"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// buildPackfile creates a packfile with all objects reachable from wants that
// are not reachable from haves.
func buildPackfile(s storer.EncodedObjectStorer, haves, wants []plumbing.Hash) ([]byte, error) {
	objects, err := revlist.Objects(s, wants, haves)
	if err != nil {
		return nil, fmt.Errorf("revlist: %w", err)
	}

	var buf bytes.Buffer
	enc := packfile.NewEncoder(&buf, s, false)
	if _, err := enc.Encode(objects, 10); err != nil {
		return nil, fmt.Errorf("encode packfile: %w", err)
	}
	return buf.Bytes(), nil
}

// applyPackfile writes the objects from a packfile into the given storer.
func applyPackfile(s storer.Storer, pack []byte) error {
	if len(pack) == 0 {
		return nil
	}
	if err := packfile.UpdateObjectStorage(s, bytes.NewReader(pack)); err != nil {
		return fmt.Errorf("update object storage: %w", err)
	}
	return nil
}
