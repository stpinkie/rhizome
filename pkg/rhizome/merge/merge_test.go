package merge

import (
	"bytes"
	"strings"
	"testing"
)

func TestThreeWayFileClean(t *testing.T) {
	base := "line1\nline2\nline3\n"
	ours := "line1\nline2 modified\nline3\n"
	theirs := "line1\nline2\nline3 modified\n"

	merged, conflict, err := ThreeWayFile([]byte(base), []byte(ours), []byte(theirs), "ours", "theirs")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if conflict {
		t.Fatalf("expected clean merge, got conflict")
	}

	want := "line1\nline2 modified\nline3 modified\n"
	if string(merged) != want {
		t.Fatalf("unexpected merge result:\n got %q\n want %q", merged, want)
	}
}

func TestThreeWayFileConflict(t *testing.T) {
	base := "line1\nline2\nline3\n"
	ours := "line1\nline2 ours\nline3\n"
	theirs := "line1\nline2 theirs\nline3\n"

	merged, conflict, err := ThreeWayFile([]byte(base), []byte(ours), []byte(theirs), "ours", "theirs")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !conflict {
		t.Fatalf("expected conflict")
	}

	if !strings.Contains(string(merged), "<<<<<<< ours") {
		t.Fatalf("missing ours conflict marker")
	}
	if !strings.Contains(string(merged), ">>>>>>> theirs") {
		t.Fatalf("missing theirs conflict marker")
	}
	if !strings.Contains(string(merged), "======") {
		t.Fatalf("missing ancestor separator")
	}
}

func TestThreeWayFileCRLF(t *testing.T) {
	base := "line1\r\nline2\r\nline3\r\n"
	ours := "line1\r\nline2 ours\r\nline3\r\n"
	theirs := "line1\r\nline2\r\nline3 theirs\r\n"

	merged, conflict, err := ThreeWayFile([]byte(base), []byte(ours), []byte(theirs), "ours", "theirs")
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if conflict {
		t.Fatalf("expected clean merge, got conflict")
	}

	// The result should keep CRLF endings because ours used CRLF.
	if !bytes.Contains(merged, []byte("\r\n")) {
		t.Fatalf("CRLF endings were not preserved")
	}

	want := "line1\r\nline2 ours\r\nline3 theirs\r\n"
	if string(merged) != want {
		t.Fatalf("unexpected merge result:\n got %q\n want %q", merged, want)
	}
}
