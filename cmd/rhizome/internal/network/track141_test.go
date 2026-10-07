package network

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// Track 141 — `rhizome mesh economy` / `rhizome network economy` verbs.

// setupEconomyHome points RHIZOME_HOME at a tempdir and returns it.
func setupEconomyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	return home
}

// seedIdentity persists a fresh node identity under home/identity.
func seedIdentity(t *testing.T, home string) *identity.Derived {
	t.Helper()
	d := testutil.NewIdentity(t)
	require.NoError(t, identity.Save(filepath.Join(home, "identity"), d, "test-node"))
	return d
}

// seedLedger writes entries into <home>/economy-ledger.jsonl.
func seedLedger(t *testing.T, home string, entries []econ.Entry) *econ.Ledger {
	t.Helper()
	l, err := econ.OpenLedger(filepath.Join(home, econ.LedgerFileName))
	require.NoError(t, err)
	for _, e := range entries {
		_, err := l.Record(e)
		require.NoError(t, err)
	}
	return l
}

// runEconomy executes the economy root with the given args and returns
// stdout + the RunE error.
func runEconomy(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewEconomyCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func entry(peerID string, dir econ.Direction, unit, amount string) econ.Entry {
	return econ.Entry{
		PeerID:    peerID,
		Direction: dir,
		Unit:      unit,
		Amount:    amount,
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	}
}

func TestEconomyCommandRegistered(t *testing.T) {
	root := NewEconomyCommand()
	for _, want := range []string{"balance", "ledger", "invoice", "settle", "dispute", "resolve"} {
		found := false
		for _, sub := range root.Commands() {
			if sub.Name() == want {
				found = true
				break
			}
		}
		require.True(t, found, "economy subcommand %q missing", want)
	}
}

func TestEconomyCommandMirrored(t *testing.T) {
	// Both `network` and `mesh` trees expose the same economy group.
	for _, tree := range []*struct {
		name string
		find func() bool
	}{
		{"network", func() bool {
			for _, c := range NewNetworkCommand().Commands() {
				if c.Name() == "economy" {
					return true
				}
			}
			return false
		}},
	} {
		require.True(t, tree.find(), "economy missing from %s tree", tree.name)
	}
}

func TestEconomyBalanceJSON(t *testing.T) {
	home := setupEconomyHome(t)
	peerA := testutil.NewIdentity(t).PeerID
	peerB := testutil.NewIdentity(t).PeerID
	seedLedger(t, home, []econ.Entry{
		entry(peerA, econ.DirectionPayable, "credits", "0.5"),
		entry(peerA, econ.DirectionReceivable, "credits", "1.25"),
		entry(peerB, econ.DirectionPayable, "credits", "0.25"),
	})

	out, err := runEconomy(t, "balance", "--json")
	require.NoError(t, err)
	var bals map[string][]econ.UnitBalance
	require.NoError(t, json.Unmarshal([]byte(out), &bals))
	require.Len(t, bals, 2)
	require.Len(t, bals[peerA], 1)
	require.Equal(t, "0.5", bals[peerA][0].PayableAccrued)
	require.Equal(t, "1.25", bals[peerA][0].ReceivableAccrued)

	// Per-peer filter.
	out, err = runEconomy(t, "balance", peerB, "--json")
	require.NoError(t, err)
	bals = nil
	require.NoError(t, json.Unmarshal([]byte(out), &bals))
	require.Len(t, bals, 1)
	require.Equal(t, "0.25", bals[peerB][0].PayableAccrued)
}

func TestEconomyBalanceRejectsBadPeer(t *testing.T) {
	setupEconomyHome(t)
	_, err := runEconomy(t, "balance", "not-a-peer")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid peer id")
}

func TestEconomyLedgerFilters(t *testing.T) {
	home := setupEconomyHome(t)
	peerA := testutil.NewIdentity(t).PeerID
	peerB := testutil.NewIdentity(t).PeerID
	old := entry(peerA, econ.DirectionPayable, "credits", "0.5")
	old.TS = time.Now().Add(-72 * time.Hour)
	old.TaskID = "task-old"
	fresh := entry(peerB, econ.DirectionReceivable, "credits", "0.25")
	fresh.TaskID = "task-new"
	fresh.State = econ.StateSettled
	seedLedger(t, home, []econ.Entry{old, fresh})

	// --peer filter.
	out, err := runEconomy(t, "ledger", "--peer", peerA, "--json")
	require.NoError(t, err)
	var got []econ.Entry
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	require.Equal(t, "task-old", got[0].TaskID)

	// --state filter.
	out, err = runEconomy(t, "ledger", "--state", "settled", "--json")
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	require.Equal(t, "task-new", got[0].TaskID)

	// --since filter (duration).
	out, err = runEconomy(t, "ledger", "--since", "24h", "--json")
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	require.Equal(t, "task-new", got[0].TaskID)

	// --since filter (RFC3339).
	out, err = runEconomy(t, "ledger", "--since", old.TS.Add(-time.Hour).Format(time.RFC3339), "--json")
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
}

func TestEconomyLedgerRejectsBadState(t *testing.T) {
	setupEconomyHome(t)
	_, err := runEconomy(t, "ledger", "--state", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid state")
}

func TestEconomyInvoiceSigns(t *testing.T) {
	home := setupEconomyHome(t)
	d := seedIdentity(t, home)
	payee := testutil.NewIdentity(t).PeerID
	a := entry(payee, econ.DirectionPayable, "credits", "0.5")
	a.TaskID = "task-1"
	b := entry(payee, econ.DirectionPayable, "credits", "1.25")
	b.TaskID = "task-2"
	seedLedger(t, home, []econ.Entry{a, b})

	out, err := runEconomy(t, "invoice", payee)
	require.NoError(t, err)
	var offer econ.SettleOffer
	require.NoError(t, json.Unmarshal([]byte(out), &offer))

	require.Equal(t, "econ/1.0.0", offer.Protocol)
	require.Equal(t, d.PeerID, offer.Issuer)
	require.Equal(t, payee, offer.PeerID)
	require.Equal(t, "credits", offer.Unit)
	require.Equal(t, "1.75", offer.Total)
	require.Len(t, offer.Entries, 2)
	require.NotEmpty(t, offer.Signature)

	// The signed doc verifies under the issuer's libp2p pubkey.
	pub, err := crypto.UnmarshalEd25519PublicKey(d.PublicKey)
	require.NoError(t, err)
	require.NoError(t, offer.Verify(pub))

	// Tampered offer fails verification.
	offer.Total = "99"
	require.Error(t, offer.Verify(pub))
}

func TestEconomyInvoiceNoBalance(t *testing.T) {
	home := setupEconomyHome(t)
	seedIdentity(t, home)
	payee := testutil.NewIdentity(t).PeerID
	out, err := runEconomy(t, "invoice", payee)
	require.NoError(t, err)
	require.Contains(t, out, "No accrued payable")
}

func TestEconomyDisputeResolveDaemonless(t *testing.T) {
	home := setupEconomyHome(t)
	payee := testutil.NewIdentity(t).PeerID
	e := entry(payee, econ.DirectionPayable, "credits", "0.5")
	e.TaskID = "task-d1"
	l := seedLedger(t, home, []econ.Entry{e})
	entryID := l.Entries()[0].EntryID

	// Dispute daemonless (no daemon in test).
	out, err := runEconomy(t, "dispute", "task-d1", "charged twice")
	require.NoError(t, err)
	require.Contains(t, out, "updated")
	got, ok := l.Get(entryID)
	require.True(t, ok)
	require.Equal(t, econ.StateDisputed, got.State)
	require.Equal(t, "charged twice", got.Note)

	// Resolve --credit → back to accrued.
	_, err = runEconomy(t, "resolve", "task-d1", "--credit")
	require.NoError(t, err)
	got, _ = l.Get(entryID)
	require.Equal(t, econ.StateAccrued, got.State)

	// Dispute again → resolve --drop → written_off.
	_, err = runEconomy(t, "dispute", "task-d1")
	require.NoError(t, err)
	_, err = runEconomy(t, "resolve", "task-d1", "--drop")
	require.NoError(t, err)
	got, _ = l.Get(entryID)
	require.Equal(t, econ.StateWrittenOff, got.State)
}

func TestEconomyResolveFlagExclusion(t *testing.T) {
	setupEconomyHome(t)
	_, err := runEconomy(t, "resolve", "task-x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--credit or --drop")
	_, err = runEconomy(t, "resolve", "task-x", "--credit", "--drop")
	require.Error(t, err)
}

func TestEconomySettleMarkOnly(t *testing.T) {
	home := setupEconomyHome(t)
	d := seedIdentity(t, home)
	payee := testutil.NewIdentity(t).PeerID
	a := entry(payee, econ.DirectionPayable, "credits", "0.5")
	a.TaskID = "task-s1"
	b := entry(payee, econ.DirectionPayable, "credits", "0.25")
	b.TaskID = "task-s2"
	l := seedLedger(t, home, []econ.Entry{a, b})

	out, err := runEconomy(t, "settle", payee, "--mark-only", "--json")
	require.NoError(t, err)
	var resp struct {
		SettleID string            `json:"settle_id"`
		Offer    *econ.SettleOffer `json:"offer"`
		Entries  []econ.Entry      `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	require.True(t, strings.HasPrefix(resp.SettleID, "sha256:"))
	require.Len(t, resp.Entries, 2)
	for _, e := range resp.Entries {
		require.Equal(t, econ.StateSettled, e.State)
		require.Equal(t, econ.MarkOnlySettleTX, e.SettleTX)
		require.Equal(t, resp.SettleID, e.SettleID)
	}
	// Signed offer verifies under the node pubkey.
	pub, err := crypto.UnmarshalEd25519PublicKey(d.PublicKey)
	require.NoError(t, err)
	require.NoError(t, resp.Offer.Verify(pub))
	require.Equal(t, d.PeerID, resp.Offer.Issuer)

	// The settled entries stay settled on reload.
	got := l.Entries()
	for _, e := range got {
		require.Equal(t, econ.StateSettled, e.State)
	}

	// Second settle reports no open balance.
	_, err = runEconomy(t, "settle", payee, "--mark-only")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no accrued payable")
}

func TestEconomySettleRejectsBadBackend(t *testing.T) {
	home := setupEconomyHome(t)
	seedIdentity(t, home)
	payee := testutil.NewIdentity(t).PeerID
	_, err := runEconomy(t, "settle", payee, "--mark-only", "--backend", "bogus")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ledger or web3")
}

func TestEconomyDisputeNoMatch(t *testing.T) {
	setupEconomyHome(t)
	_, err := runEconomy(t, "dispute", "task-nope")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no accrued")
}

func TestEconomyLedgerResyncsExternalWrites(t *testing.T) {
	// An open ledger picks up lines appended by a second writer — the
	// daemon's index refreshes when a daemonless CLI writes.
	home := setupEconomyHome(t)
	path := filepath.Join(home, econ.LedgerFileName)
	pid := testutil.NewIdentity(t).PeerID

	daemonSide, err := econ.OpenLedger(path)
	require.NoError(t, err)
	_, err = daemonSide.Record(entry(pid, econ.DirectionPayable, "credits", "0.5"))
	require.NoError(t, err)

	cliSide, err := econ.OpenLedger(path)
	require.NoError(t, err)
	require.Len(t, cliSide.Entries(), 1)
	id := cliSide.Entries()[0].EntryID
	_, err = cliSide.Transition(id, econ.StateDisputed, "", "", "cli")
	require.NoError(t, err)

	// The daemon-side handle sees the external transition on next access.
	got, ok := daemonSide.Get(id)
	require.True(t, ok)
	require.Equal(t, econ.StateDisputed, got.State)
	require.Equal(t, "cli", got.Note)
}

func TestEconomySettleNonMarkOnlyNeedsDaemon(t *testing.T) {
	home := setupEconomyHome(t)
	seedIdentity(t, home)
	payee := testutil.NewIdentity(t).PeerID
	seedLedger(t, home, []econ.Entry{
		entry(payee, econ.DirectionPayable, "credits", "0.5"),
	})
	// No daemon → the handshake path reports the missing daemon.
	_, err := runEconomy(t, "settle", payee)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no running daemon")
}
