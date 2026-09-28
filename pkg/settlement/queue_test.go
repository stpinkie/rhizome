package settlement

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

var errFakeRPC = errors.New("rpc boom")

// The QueuedSender contract: SendTx lands in the shared pending store and
// blocks until a human resolution — approved+sent returns the stamped tx
// hash; rejected/expired/failed surface as errors. Reads bypass the
// queue entirely.

func queuedSenderFixture(t *testing.T) (*QueuedSender, *web3.PendingStore) {
	t.Helper()
	store := web3.OpenPendingStore(t.TempDir())
	// The read endpoint is never dialed in these tests — sends are the
	// only surface exercised.
	client := web3.NewClient("http://127.0.0.1:9", "", nil)
	s := NewQueuedSender(store, client, testBuyer, SepoliaChainID)
	s.SetPoll(2 * time.Millisecond)
	s.SetApprovalTimeout(5 * time.Second)
	return s, store
}

func TestQueuedSender_SubmitApproveSend(t *testing.T) {
	s, store := queuedSenderFixture(t)
	var submitted string
	s.SetOnSubmit(func(e *web3.PendingEntry) { submitted = e.ID })
	var gotSummary string
	s.SetSummary(func(to string, data []byte) string {
		gotSummary = "buy open → " + to
		return gotSummary
	})

	type res struct {
		tx  string
		err error
	}
	done := make(chan res, 1)
	go func() {
		tx, err := s.SendTx(context.Background(), testFactory,
			[]byte{0xde, 0xad, 0xbe, 0xef}, nil)
		done <- res{tx, err}
	}()

	// The entry must be queued with the contract-call shape before any
	// resolution — approver sees to/selector/summary/chain.
	var id string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && submitted == "" {
		time.Sleep(2 * time.Millisecond)
	}
	id = submitted
	if id == "" {
		t.Fatal("SendTx never submitted a pending entry")
	}
	e, err := store.Get(id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if e.Kind != web3.KindContract {
		t.Fatalf("kind %q", e.Kind)
	}
	if e.To != testFactory || e.From != testBuyer {
		t.Fatalf("to/from %s/%s", e.To, e.From)
	}
	if e.Selector != "0xdeadbeef" {
		t.Fatalf("selector %q", e.Selector)
	}
	if e.ChainID != SepoliaChainID {
		t.Fatalf("chain %d", e.ChainID)
	}
	if e.Summary != gotSummary || !strings.Contains(gotSummary, testFactory) {
		t.Fatalf("summary %q", e.Summary)
	}

	// Human approves; executor broadcasts → StatusSent with the hash.
	if _, err := store.Resolve(id, true, "tester"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := store.Complete(id, "0xdeadbeef01", "", nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("SendTx: %v", r.err)
		}
		if r.tx != "0xdeadbeef01" {
			t.Fatalf("tx %q", r.tx)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendTx never resolved")
	}
}

func TestQueuedSender_Rejected(t *testing.T) {
	s, store := queuedSenderFixture(t)
	var submitted string
	s.SetOnSubmit(func(e *web3.PendingEntry) { submitted = e.ID })
	done := make(chan error, 1)
	go func() {
		_, err := s.SendTx(context.Background(), testFactory, []byte{1, 2, 3, 4}, nil)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && submitted == "" {
		time.Sleep(2 * time.Millisecond)
	}
	if submitted == "" {
		t.Fatal("no pending entry")
	}
	if _, err := store.Resolve(submitted, false, "tester"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "rejected") {
			t.Fatalf("want rejection error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendTx never resolved")
	}
}

func TestQueuedSender_ExecutorFailure(t *testing.T) {
	s, store := queuedSenderFixture(t)
	var submitted string
	s.SetOnSubmit(func(e *web3.PendingEntry) { submitted = e.ID })
	done := make(chan error, 1)
	go func() {
		_, err := s.SendTx(context.Background(), testFactory, []byte{1, 2, 3, 4}, nil)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && submitted == "" {
		time.Sleep(2 * time.Millisecond)
	}
	if _, err := store.Resolve(submitted, true, "tester"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// The executor attempted broadcast and failed → StatusFailed + error.
	if _, err := store.Complete(submitted, "", "", errFakeRPC); err != nil {
		t.Fatalf("complete: %v", err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "failed") {
			t.Fatalf("want failure error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendTx never resolved")
	}
}

func TestQueuedSender_Timeout(t *testing.T) {
	s, _ := queuedSenderFixture(t)
	s.SetApprovalTimeout(50 * time.Millisecond)
	_, err := s.SendTx(context.Background(), testFactory, []byte{1}, nil)
	if err == nil {
		t.Fatal("unapproved send should time out")
	}
}

func TestQueuedSender_NoStore(t *testing.T) {
	s := NewQueuedSender(nil, nil, testBuyer, 1)
	if _, err := s.SendTx(context.Background(), testFactory, []byte{1}, nil); err == nil {
		t.Fatal("nil store should refuse")
	}
}

func TestQueuedSender_CrossPurchaseAttribution(t *testing.T) {
	// Two purchases share a store; each sender's onSub attributes its own
	// pending ids — the buy path records them per purchase.
	_, store := queuedSenderFixture(t)
	var aIDs, bIDs []string
	a := NewQueuedSender(store, nil, testBuyer, SepoliaChainID)
	a.SetPoll(2 * time.Millisecond)
	a.SetApprovalTimeout(100 * time.Millisecond)
	a.SetOnSubmit(func(e *web3.PendingEntry) { aIDs = append(aIDs, e.ID) })
	b := NewQueuedSender(store, nil, testBuyer, SepoliaChainID)
	b.SetPoll(2 * time.Millisecond)
	b.SetApprovalTimeout(100 * time.Millisecond)
	b.SetOnSubmit(func(e *web3.PendingEntry) { bIDs = append(bIDs, e.ID) })
	_, _ = a.SendTx(context.Background(), testFactory, []byte{1}, big.NewInt(0))
	_, _ = b.SendTx(context.Background(), testFactory, []byte{2}, big.NewInt(0))
	if len(aIDs) != 1 || len(bIDs) != 1 || aIDs[0] == bIDs[0] {
		t.Fatalf("attribution ids a=%v b=%v", aIDs, bIDs)
	}
	// Both entries landed in the same shared queue file.
	list, err := store.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("store list %v %v", list, err)
	}
}
