package merge

import (
	"fmt"
	"io"
	"sort"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

// MergeTrees performs a three-way merge of git tree objects and returns the
// hash of a new tree containing the merged result, plus a list of paths that
// had conflicts.
func MergeTrees(s storer.EncodedObjectStorer, base, ours, theirs plumbing.Hash) (plumbing.Hash, []string, error) {
	baseTree, err := getTree(s, base)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("load base tree: %w", err)
	}
	oursTree, err := getTree(s, ours)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("load ours tree: %w", err)
	}
	theirsTree, err := getTree(s, theirs)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("load theirs tree: %w", err)
	}

	hash, conflicts, err := mergeTree(s, baseTree, oursTree, theirsTree, "")
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	return hash, conflicts, nil
}

func getTree(s storer.EncodedObjectStorer, h plumbing.Hash) (*object.Tree, error) {
	if h == plumbing.ZeroHash {
		return &object.Tree{}, nil
	}
	return object.GetTree(s, h)
}

func mergeTree(s storer.EncodedObjectStorer, base, ours, theirs *object.Tree, prefix string) (plumbing.Hash, []string, error) {
	entries := make(map[string]*resolvedEntry)
	names := make(map[string]struct{})

	collectNames(base, names)
	collectNames(ours, names)
	collectNames(theirs, names)

	var conflicts []string

	for name := range names {
		path := name
		if prefix != "" {
			path = prefix + "/" + name
		}

		b := findEntry(base, name)
		o := findEntry(ours, name)
		t := findEntry(theirs, name)

		res, conflict, err := resolveEntry(s, b, o, t, path)
		if err != nil {
			return plumbing.ZeroHash, nil, fmt.Errorf("resolve %q: %w", path, err)
		}
		if res != nil {
			entries[name] = res
		}
		if conflict {
			conflicts = append(conflicts, path)
		}
	}

	return buildTree(s, entries, conflicts)
}

func collectNames(t *object.Tree, names map[string]struct{}) {
	if t == nil {
		return
	}
	for _, e := range t.Entries {
		names[e.Name] = struct{}{}
	}
}

func findEntry(t *object.Tree, name string) *object.TreeEntry {
	if t == nil {
		return nil
	}
	for i := range t.Entries {
		if t.Entries[i].Name == name {
			return &t.Entries[i]
		}
	}
	return nil
}

type resolvedEntry struct {
	Name string
	Mode filemode.FileMode
	Hash plumbing.Hash
}

func resolveEntry(s storer.EncodedObjectStorer, base, ours, theirs *object.TreeEntry, path string) (*resolvedEntry, bool, error) {
	// All sides agree.
	if hashesEqual(base, ours) && hashesEqual(base, theirs) {
		return fromEntry(ours), false, nil
	}

	// If all three are missing, nothing to do.
	if ours == nil && theirs == nil && base == nil {
		return nil, false, nil
	}

	// If only one side exists, take it.
	if ours == nil && theirs == nil {
		return nil, false, nil // base existed, both deleted
	}
	if base == nil && ours == nil {
		return fromEntry(theirs), false, nil
	}
	if base == nil && theirs == nil {
		return fromEntry(ours), false, nil
	}

	// Base existed; one side deleted, the other is unchanged -> delete.
	if ours == nil && equalEntries(base, theirs) {
		return nil, false, nil
	}
	if theirs == nil && equalEntries(base, ours) {
		return nil, false, nil
	}

	// Base existed; one side deleted, the other modified -> take modified.
	if ours == nil {
		return fromEntry(theirs), false, nil
	}
	if theirs == nil {
		return fromEntry(ours), false, nil
	}

	// All three exist as trees: recurse.
	if isTree(ours) && isTree(theirs) && (base == nil || isTree(base)) {
		baseTree, _ := getTreeForEntry(s, base)
		oursTree, _ := getTreeForEntry(s, ours)
		theirsTree, _ := getTreeForEntry(s, theirs)

		childHash, childConflicts, err := mergeTree(s, baseTree, oursTree, theirsTree, path)
		if err != nil {
			return nil, false, err
		}

		return &resolvedEntry{Name: ours.Name, Mode: filemode.Dir, Hash: childHash}, len(childConflicts) > 0, nil
	}

	// All three are blobs.
	if isBlob(ours) && isBlob(theirs) && (base == nil || isBlob(base)) {
		// Fast paths.
		if ours.Hash == theirs.Hash {
			return fromEntry(ours), false, nil
		}
		if base != nil && ours.Hash == base.Hash {
			return fromEntry(theirs), false, nil
		}
		if base != nil && theirs.Hash == base.Hash {
			return fromEntry(ours), false, nil
		}

		baseContent := []byte{}
		if base != nil {
			c, err := blobContent(s, base.Hash)
			if err != nil {
				return nil, false, err
			}
			baseContent = c
		}
		oursContent, err := blobContent(s, ours.Hash)
		if err != nil {
			return nil, false, err
		}
		theirsContent, err := blobContent(s, theirs.Hash)
		if err != nil {
			return nil, false, err
		}

		merged, conflict, err := ThreeWayFile(baseContent, oursContent, theirsContent, "ours", "theirs")
		if err != nil {
			return nil, false, fmt.Errorf("merge %q: %w", path, err)
		}

		mergedHash, err := storeBlob(s, merged)
		if err != nil {
			return nil, false, fmt.Errorf("store merged blob: %w", err)
		}

		return &resolvedEntry{Name: ours.Name, Mode: ours.Mode, Hash: mergedHash}, conflict, nil
	}

	// Mixed modes (tree vs blob) or other conflicts: take ours and report conflict.
	return fromEntry(ours), true, nil
}

func isTree(e *object.TreeEntry) bool {
	if e == nil {
		return false
	}
	return e.Mode == filemode.Dir
}

func isBlob(e *object.TreeEntry) bool {
	if e == nil {
		return false
	}
	return e.Mode != filemode.Dir && e.Mode != filemode.Submodule
}

func equalEntries(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Mode == b.Mode && a.Hash == b.Hash
}

func hashesEqual(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Hash == b.Hash
}

func fromEntry(e *object.TreeEntry) *resolvedEntry {
	if e == nil {
		return nil
	}
	return &resolvedEntry{Name: e.Name, Mode: e.Mode, Hash: e.Hash}
}

func getTreeForEntry(s storer.EncodedObjectStorer, e *object.TreeEntry) (*object.Tree, error) {
	if e == nil {
		return &object.Tree{}, nil
	}
	return object.GetTree(s, e.Hash)
}

func blobContent(s storer.EncodedObjectStorer, h plumbing.Hash) ([]byte, error) {
	blob, err := object.GetBlob(s, h)
	if err != nil {
		return nil, err
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func storeBlob(s storer.EncodedObjectStorer, content []byte) (plumbing.Hash, error) {
	obj := &plumbing.MemoryObject{}
	obj.SetType(plumbing.BlobObject)
	if _, err := obj.Write(content); err != nil {
		return plumbing.ZeroHash, err
	}
	return s.SetEncodedObject(obj)
}

func buildTree(s storer.EncodedObjectStorer, entries map[string]*resolvedEntry, conflicts []string) (plumbing.Hash, []string, error) {
	var treeEntries []object.TreeEntry
	for _, e := range entries {
		treeEntries = append(treeEntries, object.TreeEntry{
			Name: e.Name,
			Mode: e.Mode,
			Hash: e.Hash,
		})
	}

	sort.Sort(object.TreeEntrySorter(treeEntries))

	tree := &object.Tree{Entries: treeEntries}
	obj := &plumbing.MemoryObject{}
	obj.SetType(plumbing.TreeObject)
	if err := tree.Encode(obj); err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("encode tree: %w", err)
	}

	hash, err := s.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("store tree: %w", err)
	}
	return hash, conflicts, nil
}
