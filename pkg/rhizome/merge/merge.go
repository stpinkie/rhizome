package merge

import (
	"fmt"
	"strings"

	"github.com/CivNode/diff3-go"
)

// ThreeWayFile performs a three-way line-level merge of two versions of a file
// against their common ancestor.
//
// It returns the merged content, a flag indicating whether any conflict markers
// were emitted, and any internal error.
func ThreeWayFile(base, ours, theirs []byte, oursLabel, theirsLabel string) ([]byte, bool, error) {
	baseNorm, _ := normalizeLineEndings(string(base))
	oursNorm, ourEnding := normalizeLineEndings(string(ours))
	theirsNorm, _ := normalizeLineEndings(string(theirs))

	opts := diff3.Options{
		Mode:           diff3.LineAware,
		MarkerLeft:     fmt.Sprintf("<<<<<<< %s", oursLabel),
		MarkerAncestor: "=======",
		MarkerRight:    fmt.Sprintf(">>>>>>> %s", theirsLabel),
	}

	result, hadConflicts, err := diff3.Merge(baseNorm, oursNorm, theirsNorm, opts)
	if err != nil {
		return nil, false, fmt.Errorf("diff3 merge: %w", err)
	}

	output := restoreLineEndings(result, ourEnding)
	return []byte(output), hadConflicts, nil
}

// normalizeLineEndings converts CRLF to LF and returns the dominant line ending
// so it can be restored later.
func normalizeLineEndings(s string) (string, string) {
	crlfCount := strings.Count(s, "\r\n")
	lfCount := strings.Count(s, "\n") - crlfCount

	if crlfCount > 0 && crlfCount >= lfCount {
		return strings.ReplaceAll(s, "\r\n", "\n"), "\r\n"
	}
	return s, "\n"
}

// restoreLineEndings applies the given line ending to a string that currently
// uses LF.
func restoreLineEndings(s, ending string) string {
	if ending == "\n" {
		return s
	}
	return strings.ReplaceAll(s, "\n", ending)
}
