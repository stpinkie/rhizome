package peeradverts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecordLoadRoundtrip(t *testing.T) {
	home := t.TempDir()
	adv := map[string]json.RawMessage{
		"rhizome-market": json.RawMessage(`{"v":1,"offers":[{"id":"o1"}]}`),
	}
	if err := Record(home, "peer-a", true, adv); err != nil {
		t.Fatalf("record: %v", err)
	}
	rows, err := Load(home)
	if err != nil || len(rows) != 1 {
		t.Fatalf("load: %v rows=%v", err, rows)
	}
	if rows[0].PeerID != "peer-a" || !rows[0].Trusted {
		t.Fatalf("row %+v", rows[0])
	}
	if _, ok := rows[0].Adverts["rhizome-market"]; !ok {
		t.Fatal("advert missing")
	}

	// Update replaces, empty clears.
	adv2 := map[string]json.RawMessage{"m2": json.RawMessage(`{"v":1}`)}
	if err := Record(home, "peer-a", false, adv2); err != nil {
		t.Fatal(err)
	}
	rows, _ = Load(home)
	if len(rows) != 1 || rows[0].Trusted {
		t.Fatalf("update: %+v", rows)
	}
	if _, ok := rows[0].Adverts["m2"]; !ok {
		t.Fatal("advert not replaced")
	}
	if err := Record(home, "peer-a", false, nil); err != nil {
		t.Fatal(err)
	}
	rows, _ = Load(home)
	if len(rows) != 1 || rows[0].Adverts != nil {
		t.Fatalf("clear: %+v", rows)
	}
}

func TestLoad_MissingAndCorrupt(t *testing.T) {
	home := t.TempDir()
	rows, err := Load(home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing should be empty-not-error: %v %v", rows, err)
	}
	if err := os.WriteFile(
		filepath.Join(home, FileName), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err = Load(home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("corrupt should be empty-not-error: %v %v", rows, err)
	}
}

func TestRecord_BoundsAndSweeps(t *testing.T) {
	home := t.TempDir()
	// Over the cap → oldest rows dropped.
	for i := 0; i < maxPeers+5; i++ {
		pid := "peer-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := Record(home, pid, false, map[string]json.RawMessage{
			"m": json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	rows, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != maxPeers {
		t.Fatalf("bound: %d rows", len(rows))
	}
}

func TestRow_ExpiryHorizon(t *testing.T) {
	r := Row{ReceivedAt: time.Now().Add(-8 * 24 * time.Hour)}
	if !r.Expired(time.Now()) {
		t.Fatal("8-day-old row should be expired")
	}
	r.ReceivedAt = time.Now()
	if r.Expired(time.Now()) {
		t.Fatal("fresh row expired")
	}
}

func TestRecord_DisabledOnEmptyHome(t *testing.T) {
	if err := Record("", "peer", true, map[string]json.RawMessage{
		"m": json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("empty home must disable, not error: %v", err)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows synthesizes POSIX modes — perm assertion is unix-only")
	}
	home := t.TempDir()
	if err := Record(home, "peer", true, map[string]json.RawMessage{
		"m": json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode %v — peer adverts are not world-readable", st.Mode().Perm())
	}
}
