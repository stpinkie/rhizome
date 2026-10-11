package econ

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

// Track 141 — settle-offer doc + mark-only / dispute / resolve cores.

func mkPeerID(t *testing.T) string {
	t.Helper()
	_, lpriv := mkKey(t)
	pid, err := peer.IDFromPublicKey(lpriv.GetPublic())
	require.NoError(t, err)
	return pid.String()
}

func mkKey(t *testing.T) (ed25519.PrivateKey, crypto.PrivKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	lpriv, err := crypto.UnmarshalEd25519PrivateKey(priv)
	require.NoError(t, err)
	return priv, lpriv
}

func mkSigner(t *testing.T) (string, ed25519.PrivateKey, crypto.PubKey) {
	t.Helper()
	priv, lpriv := mkKey(t)
	pid, err := peer.IDFromPublicKey(lpriv.GetPublic())
	require.NoError(t, err)
	return pid.String(), priv, lpriv.GetPublic()
}

func openTmpLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := OpenLedger(filepath.Join(t.TempDir(), LedgerFileName))
	require.NoError(t, err)
	return l
}

func TestBuildOfferSumsAccruedPayables(t *testing.T) {
	l := openTmpLedger(t)
	issuer := mkPeerID(t)
	payee := mkPeerID(t)

	for _, e := range []Entry{
		{PeerID: payee, Direction: DirectionPayable, TaskID: "t1", Unit: "credits", Amount: "0.5"},
		{PeerID: payee, Direction: DirectionPayable, TaskID: "t2", Unit: "credits", Amount: "1.25"},
		{PeerID: payee, Direction: DirectionReceivable, TaskID: "t3", Unit: "credits", Amount: "9"},
		{PeerID: payee, Direction: DirectionPayable, TaskID: "t4", Unit: "usdc", Amount: "0.1"},
		{PeerID: "other-peer", Direction: DirectionPayable, TaskID: "t5", Unit: "credits", Amount: "7"},
	} {
		_, err := l.Record(e)
		require.NoError(t, err)
	}

	offer, err := BuildOffer(issuer, payee, "credits", l.Entries(), time.Now().UTC())
	require.NoError(t, err)
	require.NotNil(t, offer)
	require.Equal(t, "econ/1.0.0", offer.Protocol)
	require.Equal(t, issuer, offer.Issuer)
	require.Equal(t, payee, offer.PeerID)
	require.Equal(t, "credits", offer.Unit)
	require.Equal(t, "1.75", offer.Total)
	// Entries carry the wire refs (task ids), newest-first.
	require.Equal(t, []string{"t2", "t1"}, offer.Entries)
	require.NotEmpty(t, offer.Nonce)
	require.False(t, offer.Cutoff.IsZero())

	// A peer with no accrued payables yields no offer (not an error).
	none, err := BuildOffer(issuer, "peer-nobody", "credits", l.Entries(), time.Now().UTC())
	require.NoError(t, err)
	require.Nil(t, none)

	// Settled entries don't re-offer — settle the t2 row (the 1.25 one).
	var t2id string
	for _, e := range l.Entries() {
		if e.TaskID == "t2" {
			t2id = e.EntryID
		}
	}
	done, err := l.Transition(t2id, StateSettled, "s1", "", "")
	require.NoError(t, err)
	require.Equal(t, StateSettled, done.State)
	re, err := BuildOffer(issuer, payee, "credits", l.Entries(), time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, "0.5", re.Total)
	require.Len(t, re.Entries, 1)
}

func TestSettleOfferSignVerify(t *testing.T) {
	issuerID, priv, pub := mkSigner(t)
	offer := &SettleOffer{
		Protocol: "econ/1.0.0",
		Issuer:   issuerID,
		PeerID:   mkPeerID(t),
		Unit:     "credits",
		Entries:  []string{"e1", "e2"},
		Total:    "1.75",
		Cutoff:   time.Now().UTC().Truncate(time.Second),
		Nonce:    "abc123",
		TS:       time.Now().UTC().Truncate(time.Second),
	}
	require.NoError(t, offer.Sign(priv))
	require.NoError(t, offer.Verify(pub))

	// Tampering breaks verification.
	offer.Total = "2"
	require.Error(t, offer.Verify(pub))
	offer.Total = "1.75"

	// ID is stable for the same unsigned doc and changes on mutation.
	id1 := offer.ID()
	offer.Nonce = "other"
	require.NotEqual(t, id1, offer.ID())
	offer.Nonce = "abc123"
	require.Equal(t, id1, offer.ID())
}

func TestMarkSettledTransitionsAndSigns(t *testing.T) {
	l := openTmpLedger(t)
	issuerID, priv, pub := mkSigner(t)
	payee := mkPeerID(t)
	a, err := l.Record(Entry{PeerID: payee, Direction: DirectionPayable, TaskID: "t1", Unit: "credits", Amount: "0.5"})
	require.NoError(t, err)
	_, err = l.Record(
		Entry{PeerID: payee, Direction: DirectionReceivable, TaskID: "t2", Unit: "credits", Amount: "0.9"},
	)
	require.NoError(t, err)

	offer, entries, err := MarkSettled(l, issuerID, payee, "credits", priv)
	require.NoError(t, err)
	require.NotNil(t, offer)
	require.Len(t, entries, 1)
	require.Equal(t, a.EntryID, entries[0].EntryID)
	require.Equal(t, StateSettled, entries[0].State)
	require.Equal(t, MarkOnlySettleTX, entries[0].SettleTX)
	require.Equal(t, offer.ID(), entries[0].SettleID)
	require.NoError(t, offer.Verify(pub))

	// Receivable side untouched.
	all := l.Entries()
	var rec *Entry
	for i := range all {
		if all[i].Direction == DirectionReceivable {
			rec = &all[i]
		}
	}
	require.Equal(t, StateAccrued, rec.State)

	// Nothing left → error, not a silent no-op.
	_, _, err = MarkSettled(l, issuerID, payee, "credits", priv)
	require.Error(t, err)
}

func TestDisputeResolveTaskCores(t *testing.T) {
	l := openTmpLedger(t)
	payee := mkPeerID(t)
	_, err := l.Record(
		Entry{PeerID: payee, Direction: DirectionPayable, TaskID: "task-x", Unit: "credits", Amount: "0.5"},
	)
	require.NoError(t, err)
	// Correlation-id-only (sync-path) entries match too.
	_, err = l.Record(
		Entry{PeerID: payee, Direction: DirectionPayable, CorrelationID: "corr-9", Unit: "credits", Amount: "0.1"},
	)
	require.NoError(t, err)

	out, err := DisputeTask(l, "task-x", "charged twice")
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "charged twice", out[0].Note)

	// Correlation ref hits the sync entry.
	out, err = DisputeTask(l, "corr-9", "")
	require.NoError(t, err)
	require.Len(t, out, 1)

	// Resolve credit → accrued.
	out, err = ResolveTask(l, "task-x", false)
	require.NoError(t, err)
	require.Equal(t, StateAccrued, out[0].State)

	// Resolve drop → written_off.
	_, err = DisputeTask(l, "task-x", "")
	require.NoError(t, err)
	out, err = ResolveTask(l, "task-x", true)
	require.NoError(t, err)
	require.Equal(t, StateWrittenOff, out[0].State)

	// Unknown ref errors.
	_, err = DisputeTask(l, "task-nope", "")
	require.Error(t, err)
	_, err = ResolveTask(l, "task-nope", true)
	require.Error(t, err)
}

// Track 142 — payee-side receivable match + reconciliation digest.

func TestMatchReceivables(t *testing.T) {
	l := openTmpLedger(t)
	payer := mkPeerID(t)

	for _, e := range []Entry{
		{PeerID: payer, Direction: DirectionReceivable, TaskID: "w1", Unit: "credits", Amount: "0.5"},
		{PeerID: payer, Direction: DirectionReceivable, TaskID: "w2", Unit: "credits", Amount: "0.25"},
		// Decoys: settled, wrong unit, wrong direction, wrong peer.
		{PeerID: payer, Direction: DirectionReceivable, TaskID: "w3", Unit: "credits", Amount: "9"},
		{PeerID: payer, Direction: DirectionReceivable, TaskID: "w4", Unit: "usdc", Amount: "9"},
		{PeerID: payer, Direction: DirectionPayable, TaskID: "w5", Unit: "credits", Amount: "9"},
		{PeerID: "other", Direction: DirectionReceivable, TaskID: "w6", Unit: "credits", Amount: "9"},
	} {
		_, err := l.Record(e)
		require.NoError(t, err)
	}
	// Retire w3 so it can't match.
	var w3id string
	for _, e := range l.Entries() {
		if e.TaskID == "w3" {
			w3id = e.EntryID
		}
	}
	_, err := l.Transition(w3id, StateSettled, "s", "", "")
	require.NoError(t, err)

	offer := &SettleOffer{
		Issuer:  payer,
		Unit:    "credits",
		Entries: []string{"w1", "w2"},
		Total:   "0.75",
	}
	matched, err := MatchReceivables(l.Entries(), offer)
	require.NoError(t, err)
	require.Len(t, matched, 2)

	// Amount drift → divergence.
	offer.Total = "0.80"
	_, err = MatchReceivables(l.Entries(), offer)
	require.ErrorContains(t, err, "does not match")

	// Missing receivable → divergence.
	offer.Total = "0.75"
	offer.Entries = []string{"w1", "w-ghost"}
	_, err = MatchReceivables(l.Entries(), offer)
	require.ErrorContains(t, err, "no accrued receivable")

	// Duplicate ref in the offer → divergence.
	offer.Entries = []string{"w1", "w1"}
	_, err = MatchReceivables(l.Entries(), offer)
	require.ErrorContains(t, err, "duplicate work ref")
}

func TestEntriesDigestOrderIndependent(t *testing.T) {
	entries := []Entry{
		{PeerID: "p", TaskID: "b-task", Amount: "0.5", State: StateAccrued},
		{PeerID: "p", TaskID: "a-task", Amount: "1.0", State: StateSettled},
		{PeerID: "p", CorrelationID: "c-corr", Amount: "2", State: StateAccrued},
	}
	d1 := EntriesDigest(entries)
	d2 := EntriesDigest([]Entry{entries[2], entries[0], entries[1]})
	require.Equal(t, d1, d2)
	require.Contains(t, d1, "sha256:")

	// A state change or amount change flips the digest.
	changed := append([]Entry{}, entries...)
	changed[0].State = StateSettled
	require.NotEqual(t, d1, EntriesDigest(changed))
}
