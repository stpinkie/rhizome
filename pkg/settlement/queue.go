package settlement

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// QueuedSender is the human-gated Sender: reads go straight to the
// endpoint; every SendTx lands in the shared web3 pending-approval queue
// (<RHIZOME_HOME>/web3/web3-pending.json) and blocks until a human
// resolves it — the daemon or CLI then signs and broadcasts, stamping
// the tx hash back onto the entry. It is the default sender for real
// chains: no key material ever touches the market module, and every
// escrow mutation is an explicit human decision.
type QueuedSender struct {
	read    *DirectSender // Call/Receipt/Now go straight to the endpoint
	store   *web3.PendingStore
	from    string
	chainID uint64
	poll    time.Duration
	timeout time.Duration
	summary func(to string, data []byte) string // optional approval-line hook
	onSub   func(*web3.PendingEntry)            // optional post-Submit hook (record ids)
}

const (
	// queuedPoll is the entry-status poll cadence — cheap local file
	// reads, so a tight interval is fine.
	queuedPoll = 2 * time.Second
	// queuedTimeout bounds one approval wait; a human may take minutes
	// but a hung approval must not pin a purchase goroutine forever.
	queuedTimeout = 30 * time.Minute
)

// NewQueuedSender builds a Sender that queues contract transactions for
// human approval and reads the endpoint directly. `from` is the EVM
// address the approver signs with; chainID is stamped on entries so
// ExecuteApproved refuses to sign on the wrong network.
func NewQueuedSender(
	store *web3.PendingStore, client *web3.Client, from string, chainID uint64,
) *QueuedSender {
	return &QueuedSender{
		read: NewDirectSender(client, from), store: store,
		from: from, chainID: chainID,
		poll: queuedPoll, timeout: queuedTimeout,
	}
}

// SetPoll overrides the status poll cadence (tests).
func (s *QueuedSender) SetPoll(d time.Duration) { s.poll = d }

// SetApprovalTimeout bounds how long SendTx waits for a resolution.
func (s *QueuedSender) SetApprovalTimeout(d time.Duration) { s.timeout = d }

// SetSummary installs a hook that produces the human-readable approval
// line (e.g. "market buy: open escrow 0xabc… for 5.00 USDC").
func (s *QueuedSender) SetSummary(fn func(to string, data []byte) string) { s.summary = fn }

// SetOnSubmit installs a hook fired after each Submit — callers surface
// the pending-entry id to the operator (e.g. `market session` output).
func (s *QueuedSender) SetOnSubmit(fn func(*web3.PendingEntry)) { s.onSub = fn }

// From reports the signer address queued entries are stamped with.
func (s *QueuedSender) From() string { return s.from }

// Call performs eth_call against the endpoint — reads are not gated.
func (s *QueuedSender) Call(ctx context.Context, to string, data []byte) ([]byte, error) {
	return s.read.Call(ctx, to, data)
}

// Receipt fetches a mined receipt (nil while pending).
func (s *QueuedSender) Receipt(ctx context.Context, txHash string) (*web3.Receipt, error) {
	return s.read.Receipt(ctx, txHash)
}

// Now returns the latest block's unix timestamp.
func (s *QueuedSender) Now(ctx context.Context) (int64, error) {
	return s.read.Now(ctx)
}

// SendTx enqueues a KindContract pending entry and polls until it is
// broadcast (StatusSent → tx hash) or terminally unresolved. Rejection,
// expiry, and executor failures surface as errors — a purchase never
// proceeds on an unapproved transaction.
func (s *QueuedSender) SendTx(
	ctx context.Context, to string, data []byte, valueWei *big.Int,
) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("settlement: queued sender has no pending store")
	}
	e := &web3.PendingEntry{
		Kind:    web3.KindContract,
		ChainID: s.chainID,
		From:    s.from,
		To:      to,
		Data:    "0x" + hex.EncodeToString(data),
		Summary: s.approvalSummary(to, data),
	}
	if len(data) >= 4 {
		e.Selector = "0x" + hex.EncodeToString(data[:4])
	}
	if valueWei != nil && valueWei.Sign() > 0 {
		e.ValueWei = valueWei.String()
	}
	sub, err := s.store.Submit(e)
	if err != nil {
		return "", fmt.Errorf("queue settlement tx: %w", err)
	}
	if s.onSub != nil {
		s.onSub(sub)
	}

	wait := s.timeout
	if wait <= 0 {
		wait = queuedTimeout
	}
	poll := s.poll
	if poll <= 0 {
		poll = queuedPoll
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		got, err := s.store.Get(sub.ID)
		if err != nil {
			return "", fmt.Errorf("poll pending %s: %w", sub.ID, err)
		}
		switch got.Status {
		case web3.StatusSent:
			return got.TxHash, nil
		case web3.StatusRejected:
			return "", fmt.Errorf("settlement tx %s rejected by approver", sub.ID)
		case web3.StatusExpired:
			return "", fmt.Errorf("settlement tx %s expired awaiting approval", sub.ID)
		case web3.StatusFailed:
			return "", fmt.Errorf("settlement tx %s failed: %s", sub.ID, got.Error)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("settlement tx %s: %w", sub.ID, ctx.Err())
		case <-time.After(poll):
		}
	}
}

func (s *QueuedSender) approvalSummary(to string, data []byte) string {
	if s.summary != nil {
		if line := s.summary(to, data); line != "" {
			return line
		}
	}
	sel := ""
	if len(data) >= 4 {
		sel = " selector 0x" + hex.EncodeToString(data[:4])
	}
	return fmt.Sprintf("rhizome-market contract call%s to %s", sel, to)
}
