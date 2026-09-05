package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/libp2p/go-libp2p/core/peer"

	rhizomeconfig "github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/fileutil"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/merge"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

var defaultSyncTimeouts = rhizomeconfig.DefaultTimeouts().Sync

// SyncError records a single sync failure with a timestamp.
type SyncError struct {
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// SyncStatus is the persisted snapshot exposed by rhizome sync status.
type SyncStatus struct {
	LastSyncError string            `json:"last_sync_error,omitempty"`
	LastErrorTime time.Time         `json:"last_error_time,omitempty"`
	PeerHeads     map[string]string `json:"peer_heads,omitempty"`
}

type pullState struct {
	done chan struct{}
	err  error
}

// Syncer owns the workspace git repository and coordinates P2P sync.
type Syncer struct {
	repo      *git.Repository
	worktree  *git.Worktree
	node      *network.Node
	nodeName  string
	transport *Transport
	exclude   []string

	peerHeads   map[peer.ID]plumbing.Hash
	peerHeadsMu sync.RWMutex

	pulling   map[peer.ID]*pullState
	pullingMu sync.Mutex

	autoSync         bool
	commitInterval   time.Duration
	announceInterval time.Duration
	timeouts         *rhizomeconfig.SyncTimeouts

	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex

	watcher      *Watcher
	ticker       *time.Ticker
	commitTicker *time.Ticker
	stop         chan struct{}
	wg           sync.WaitGroup

	syncStatusPath string

	lastSyncErrMu sync.RWMutex
	lastSyncError SyncError
}

// Config controls syncer behavior.
type Config struct {
	Workspace        string
	NodeName         string
	Node             *network.Node
	AutoSync         bool
	CommitInterval   time.Duration
	AnnounceInterval time.Duration
	Exclude          []string
	// Timeouts override built-in defaults. A nil value uses defaults.
	Timeouts *rhizomeconfig.SyncTimeouts
}

// NewSyncer opens the workspace repository and creates a syncer.
func NewSyncer(ctx context.Context, cfg Config) (*Syncer, error) {
	repo, w, err := OpenOrInit(cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace repo: %w", err)
	}

	commitInterval := cfg.CommitInterval
	announceInterval := cfg.AnnounceInterval
	syncTimeouts := cfg.Timeouts
	if syncTimeouts == nil {
		syncTimeouts = &defaultSyncTimeouts
	}
	if commitInterval <= 0 {
		if syncTimeouts.CommitInterval > 0 {
			commitInterval = syncTimeouts.CommitInterval.Duration()
		} else {
			commitInterval = 2 * time.Second
		}
	}
	if announceInterval <= 0 {
		if syncTimeouts.AnnounceInterval > 0 {
			announceInterval = syncTimeouts.AnnounceInterval.Duration()
		} else {
			announceInterval = 30 * time.Second
		}
	}

	s := &Syncer{
		repo:             repo,
		worktree:         w,
		node:             cfg.Node,
		nodeName:         cfg.NodeName,
		exclude:          cfg.Exclude,
		peerHeads:        make(map[peer.ID]plumbing.Hash),
		pulling:          make(map[peer.ID]*pullState),
		autoSync:         cfg.AutoSync,
		commitInterval:   commitInterval,
		announceInterval: announceInterval,
		timeouts:         syncTimeouts,
		stop:             make(chan struct{}),
	}

	s.syncStatusPath = filepath.Join(filepath.Dir(w.Filesystem.Root()), "sync-status.json")

	if cfg.Node != nil {
		s.transport = NewTransport(cfg.Node.Host(), s)
		s.transport.requestTimeout = s.transportRequestTimeout()
		s.transport.packfileTimeout = s.transportPackfileTimeout()
		s.transport.announceTimeout = s.transportRequestTimeout()
	}

	return s, nil
}

// Start begins file watching, the transport listener, and anti-entropy.
func (s *Syncer) Start(ctx context.Context) error {
	if s.node == nil || s.transport == nil {
		return fmt.Errorf("syncer: no node configured")
	}

	s.ctx, s.cancel = context.WithCancel(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.transport.Start(s.ctx)
	}()

	// Wait until the stream handler is registered before returning.
	select {
	case <-s.transport.Ready():
	case <-time.After(s.transportRequestTimeout()):
		if s.cancel != nil {
			s.cancel()
		}
		return fmt.Errorf("sync transport did not become ready")
	}

	if s.autoSync {
		var err error
		s.watcher, err = NewWatcher(s.ctx, s.worktree.Filesystem.Root(), s.exclude, func(paths []string) {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				_, _ = s.commitAndAnnounce(s.ctx)
			}()
		})
		if err != nil {
			if s.cancel != nil {
				s.cancel()
			}
			return fmt.Errorf("start watcher: %w", err)
		}

		if s.commitInterval > 0 {
			s.commitTicker = time.NewTicker(s.commitInterval)
			s.wg.Add(1)
			go s.commitLoop(s.ctx)
		}
	}

	if s.announceInterval > 0 {
		s.ticker = time.NewTicker(s.announceInterval)
		s.wg.Add(1)
		go s.antiEntropyLoop(s.ctx)
	}

	s.node.OnConnected(func(ev network.PeerEvent) {
		if ev.PeerID == s.node.ID() {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.announceToPeer(s.ctx, ev.PeerID)
		}()
	})

	for _, pid := range s.node.ConnectedPeers() {
		if pid == s.node.ID() {
			continue
		}
		s.wg.Add(1)
		go func(p peer.ID) {
			defer s.wg.Done()
			s.announceToPeer(s.ctx, p)
		}(pid)
	}

	return nil
}

func (s *Syncer) timeoutsOrDefault() *rhizomeconfig.SyncTimeouts {
	if s.timeouts != nil {
		return s.timeouts
	}
	return &defaultSyncTimeouts
}

func (s *Syncer) peerWait() time.Duration {
	if s.timeoutsOrDefault().PeerWait > 0 {
		return s.timeoutsOrDefault().PeerWait.Duration()
	}
	return 10 * time.Second
}

func (s *Syncer) fetchRetry() time.Duration {
	if s.timeoutsOrDefault().FetchRetry > 0 {
		return s.timeoutsOrDefault().FetchRetry.Duration()
	}
	return 300 * time.Millisecond
}

func (s *Syncer) fetchRetryDelay(attempt int) time.Duration {
	base := s.fetchRetry()
	for i := 0; i < attempt; i++ {
		base *= 2
		if base >= 10*time.Second {
			base = 10 * time.Second
			break
		}
	}
	jitter := 0.8 + 0.4*rand.Float64()
	d := time.Duration(float64(base) * jitter)
	if d <= 0 {
		return base
	}
	return d
}

func (s *Syncer) fetchAttemptTimeout() time.Duration {
	if s.timeoutsOrDefault().FetchAttemptTimeout > 0 {
		return s.timeoutsOrDefault().FetchAttemptTimeout.Duration()
	}
	return 60 * time.Second
}

func (s *Syncer) transportRequestTimeout() time.Duration {
	if s.timeoutsOrDefault().TransportRequest > 0 {
		return s.timeoutsOrDefault().TransportRequest.Duration()
	}
	return 30 * time.Second
}

func (s *Syncer) transportPackfileTimeout() time.Duration {
	if s.timeoutsOrDefault().TransportPackfile > 0 {
		return s.timeoutsOrDefault().TransportPackfile.Duration()
	}
	return 60 * time.Second
}

// Stop cleanly shuts down the syncer.
func (s *Syncer) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	close(s.stop)
	if s.watcher != nil {
		_ = s.watcher.Close()
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.commitTicker != nil {
		s.commitTicker.Stop()
	}
	s.wg.Wait()
	return nil
}

// PullFrom fetches and merges from a single peer.
func (s *Syncer) PullFrom(ctx context.Context, pid peer.ID) (err error) {
	s.pullingMu.Lock()
	if state, ok := s.pulling[pid]; ok {
		s.pullingMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-state.done:
		}
		return state.err
	}

	state := &pullState{done: make(chan struct{})}
	s.pulling[pid] = state
	s.pullingMu.Unlock()

	defer func() {
		s.pullingMu.Lock()
		state.err = err
		delete(s.pulling, pid)
		close(state.done)
		s.pullingMu.Unlock()
	}()

	// Ensure the peer is actually connected before opening a sync stream.
	// CI runs can be heavily loaded, so allow more time for identify/protocol
	// negotiation before failing the pull.
	if !s.waitForPeerConnection(ctx, pid, s.peerWait()) {
		err = fmt.Errorf("peer %s is not connected", pid)
		s.setLastSyncError(err, pid)
		return err
	}

	return s.pullFromLocked(ctx, pid)
}

func (s *Syncer) waitForPeerConnection(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range s.node.ConnectedPeers() {
			if p == pid {
				// Connection may be established before identify has advertised
				// the sync protocol, so wait until the peer supports it.
				protos, err := s.node.Host().Peerstore().SupportsProtocols(pid, ProtocolID)
				if err == nil && len(protos) > 0 {
					return true
				}
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

func (s *Syncer) pullFromLocked(ctx context.Context, pid peer.ID) error {
	s.mu.Lock()
	head, err := Head(s.repo)
	s.mu.Unlock()
	if err != nil {
		s.setLastSyncError(fmt.Errorf("local head: %w", err), pid)
		return fmt.Errorf("local head: %w", err)
	}

	haves := []plumbing.Hash{head}
	var pack []byte
	var remoteHead plumbing.Hash
	const retries = 5
	for i := 0; i < retries; i++ {
		pack, remoteHead, err = s.transport.Fetch(ctx, pid, haves, nil)
		if err == nil {
			break
		}
		if i == retries-1 {
			s.setLastSyncError(fmt.Errorf("fetch from %s: %w", pid, err), pid)
			return fmt.Errorf("fetch from %s: %w", pid, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.fetchRetryDelay(i)):
		}
	}

	if _, changed := s.setPeerHead(pid, remoteHead); changed {
		s.saveSyncStatus()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyPackfileAndMergeLocked(ctx, pack, remoteHead, pid); err != nil {
		return fmt.Errorf("merge from %s: %w", pid, err)
	}
	return nil
}

// PushTo commits and announces to a single peer.
func (s *Syncer) PushTo(ctx context.Context, pid peer.ID) (plumbing.Hash, error) {
	s.mu.Lock()
	head, err := s.commitAndAnnounceLocked(ctx)
	s.mu.Unlock()
	if err != nil {
		s.setLastSyncError(err, "")
		return plumbing.ZeroHash, err
	}

	ctx, cancel := context.WithTimeout(ctx, s.transportRequestTimeout())
	defer cancel()

	if err := s.transport.AnnounceHead(ctx, pid, head); err != nil {
		s.setLastSyncError(fmt.Errorf("announce head to %s: %w", pid, err), pid)
		return plumbing.ZeroHash, err
	}
	return head, nil
}

// commitAndAnnounce commits pending changes and announces the new HEAD.
func (s *Syncer) commitAndAnnounce(ctx context.Context) (plumbing.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	head, err := s.commitAndAnnounceLocked(ctx)
	if err != nil {
		s.setLastSyncError(err, "")
	}
	return head, err
}

func (s *Syncer) commitAndAnnounceLocked(ctx context.Context) (plumbing.Hash, error) {
	dirty, err := HasUncommitted(s.worktree)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("status: %w", err)
	}

	var head plumbing.Hash
	if dirty {
		head, err = Commit(s.worktree, s.nodeName, fmt.Sprintf("%s: workspace sync", s.nodeName))
		if errors.Is(err, git.ErrEmptyCommit) {
			head, err = Head(s.repo)
		}
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("commit: %w", err)
		}
	} else {
		head, err = Head(s.repo)
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}

	for _, pid := range s.node.ConnectedPeers() {
		if pid == s.node.ID() {
			continue
		}
		go func(p peer.ID) {
			ctx, cancel := context.WithTimeout(s.ctx, s.transportRequestTimeout())
			defer cancel()
			_ = s.transport.AnnounceHead(ctx, p, head)
		}(pid)
	}

	return head, nil
}

func (s *Syncer) commitLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.commitTicker.C:
			dirty, err := HasUncommitted(s.worktree)
			if err != nil {
				s.setLastSyncError(fmt.Errorf("status: %w", err), "")
				continue
			}
			if dirty {
				_, _ = s.commitAndAnnounce(s.ctx)
			}
		}
	}
}

// ProvidePackfile implements the transport Handler interface.
func (s *Syncer) ProvidePackfile(from peer.ID, haves, wants []plumbing.Hash) ([]byte, plumbing.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providePackfileLocked(from, haves, wants)
}

func (s *Syncer) providePackfileLocked(from peer.ID, haves, wants []plumbing.Hash) ([]byte, plumbing.Hash, error) {
	head, err := Head(s.repo)
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("head: %w", err)
	}

	wantsList := wants
	if len(wantsList) == 0 {
		wantsList = []plumbing.Hash{head}
	}

	pack, err := buildPackfile(s.repo.Storer, haves, wantsList)
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("build packfile: %w", err)
	}
	return pack, head, nil
}

// HandleAnnounce implements the transport Handler interface.
func (s *Syncer) HandleAnnounce(from peer.ID, head plumbing.Hash) {
	_, changed := s.setPeerHead(from, head)
	if !changed {
		return
	}
	s.saveSyncStatus()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		parent := s.ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, s.fetchAttemptTimeout())
		defer cancel()
		_ = s.PullFrom(ctx, from)
	}()
}

func (s *Syncer) antiEntropyLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.ticker.C:
			s.runAntiEntropy(ctx)
		}
	}
}

func (s *Syncer) runAntiEntropy(ctx context.Context) {
	for _, pid := range s.node.ConnectedPeers() {
		if pid == s.node.ID() {
			continue
		}
		s.wg.Add(1)
		go func(p peer.ID) {
			defer s.wg.Done()
			ctx, cancel := context.WithTimeout(ctx, s.fetchAttemptTimeout())
			defer cancel()
			_ = s.PullFrom(ctx, p)
		}(pid)
	}
}

// applyPackfileAndMergeLocked decodes a packfile and fast-forwards or merges.
// It must be called with s.mu held.
func (s *Syncer) applyPackfileAndMergeLocked(ctx context.Context, pack []byte, remoteHead plumbing.Hash, pid peer.ID) error {
	if _, err := s.commitAndAnnounceLocked(ctx); err != nil {
		s.setLastSyncError(fmt.Errorf("commit local work: %w", err), pid)
		logger.WarnCF("sync", "skipping merge: failed to commit local changes", map[string]any{
			"peer":  pid.String(),
			"error": err.Error(),
		})
		return fmt.Errorf("commit local work: %w", err)
	}

	if err := applyPackfile(s.repo.Storer, pack); err != nil {
		s.setLastSyncError(fmt.Errorf("apply packfile: %w", err), pid)
		return fmt.Errorf("apply packfile: %w", err)
	}

	ours, err := Head(s.repo)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("local head: %w", err), pid)
		return err
	}

	if ours == remoteHead || remoteHead.IsZero() {
		return nil
	}

	oCommit, err := s.repo.CommitObject(ours)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("load ours: %w", err), pid)
		return fmt.Errorf("load ours: %w", err)
	}
	tCommit, err := s.repo.CommitObject(remoteHead)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("load theirs: %w", err), pid)
		return fmt.Errorf("load theirs: %w", err)
	}

	// Fast-forward if possible.
	isAncestor, err := oCommit.IsAncestor(tCommit)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("isAncestor: %w", err), pid)
		return fmt.Errorf("isAncestor: %w", err)
	}
	if isAncestor {
		return s.fastForwardToLocked(remoteHead)
	}

	// Nothing to do if they are behind us.
	isDescendant, err := tCommit.IsAncestor(oCommit)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("isDescendant: %w", err), pid)
		return fmt.Errorf("isDescendant: %w", err)
	}
	if isDescendant {
		return nil
	}

	// Three-way merge.
	bases, err := oCommit.MergeBase(tCommit)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("merge base: %w", err), pid)
		return fmt.Errorf("merge base: %w", err)
	}
	if len(bases) == 0 {
		err := fmt.Errorf("no merge base with %s", remoteHead)
		s.setLastSyncError(err, pid)
		return err
	}
	baseCommit := bases[0]

	mergedTree, conflicts, err := merge.MergeTrees(
		s.repo.Storer,
		baseCommit.TreeHash,
		oCommit.TreeHash,
		tCommit.TreeHash,
	)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("merge trees: %w", err), pid)
		return fmt.Errorf("merge trees: %w", err)
	}

	mergeHash, err := s.createMergeCommitLocked(ours, remoteHead, mergedTree, conflicts)
	if err != nil {
		s.setLastSyncError(fmt.Errorf("create merge commit: %w", err), pid)
		return fmt.Errorf("create merge commit: %w", err)
	}

	if err := s.fastForwardToLocked(mergeHash); err != nil {
		s.setLastSyncError(fmt.Errorf("checkout merge: %w", err), pid)
		return fmt.Errorf("checkout merge: %w", err)
	}

	if len(conflicts) > 0 {
		// Conflict markers are in the tree; the user resolves them later.
		// We do not return an error so sync continues.
		_ = conflicts
	}

	return nil
}

// fastForwardToLocked updates HEAD and the worktree to the target commit.
// It must be called with s.mu held.
func (s *Syncer) fastForwardToLocked(target plumbing.Hash) error {
	if err := s.repo.Storer.SetReference(plumbing.NewHashReference(plumbing.HEAD, target)); err != nil {
		s.setLastSyncError(fmt.Errorf("set head: %w", err), peer.ID(""))
		return err
	}
	if err := s.worktree.Reset(&git.ResetOptions{Commit: target, Mode: git.HardReset}); err != nil {
		s.setLastSyncError(fmt.Errorf("checkout: %w", err), peer.ID(""))
		return err
	}
	return nil
}

func (s *Syncer) createMergeCommitLocked(
	ours, theirs plumbing.Hash,
	tree plumbing.Hash,
	conflicts []string,
) (plumbing.Hash, error) {
	msg := fmt.Sprintf("%s: merge from %s", s.nodeName, theirs.String()[:8])
	if len(conflicts) > 0 {
		msg += fmt.Sprintf("\n\nConflicts:\n")
		for _, c := range conflicts {
			msg += fmt.Sprintf("  - %s\n", c)
		}
	}

	sig := &object.Signature{
		Name:  s.nodeName,
		Email: s.nodeName + "@rhizome.local",
		When:  time.Now().UTC(),
	}

	c := &object.Commit{
		TreeHash:     tree,
		ParentHashes: []plumbing.Hash{ours, theirs},
		Author:       *sig,
		Committer:    *sig,
		Message:      msg,
	}

	obj := &plumbing.MemoryObject{}
	obj.SetType(plumbing.CommitObject)
	if err := c.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return s.repo.Storer.SetEncodedObject(obj)
}

func (s *Syncer) announceToPeer(ctx context.Context, pid peer.ID) {
	if s.node == nil {
		return
	}
	if !s.waitForPeerConnection(ctx, pid, s.peerWait()) {
		return
	}

	s.mu.Lock()
	head, err := Head(s.repo)
	s.mu.Unlock()
	if err != nil {
		s.setLastSyncError(fmt.Errorf("head: %w", err), pid)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, s.transportRequestTimeout())
	defer cancel()

	if err := s.transport.AnnounceHead(ctx, pid, head); err != nil {
		s.setLastSyncError(fmt.Errorf("announce head to %s: %w", pid, err), pid)
	}
}

// LastSyncError returns the most recent sync failure and its timestamp.
func (s *Syncer) LastSyncError() SyncError {
	s.lastSyncErrMu.RLock()
	defer s.lastSyncErrMu.RUnlock()
	return s.lastSyncError
}

// PeerHeads returns a copy of the last known HEAD for each peer.
func (s *Syncer) PeerHeads() map[string]string {
	s.peerHeadsMu.RLock()
	defer s.peerHeadsMu.RUnlock()
	out := make(map[string]string, len(s.peerHeads))
	for k, v := range s.peerHeads {
		out[k.String()] = v.String()
	}
	return out
}

func (s *Syncer) setLastSyncError(err error, pid peer.ID) {
	if err == nil {
		return
	}
	s.lastSyncErrMu.Lock()
	s.lastSyncError = SyncError{Message: err.Error(), Time: time.Now().UTC()}
	s.lastSyncErrMu.Unlock()

	logger.WarnCF("sync", "sync error", map[string]any{
		"peer":  pid.String(),
		"error": err.Error(),
	})
	s.saveSyncStatus()
}

func (s *Syncer) setPeerHead(pid peer.ID, head plumbing.Hash) (plumbing.Hash, bool) {
	s.peerHeadsMu.Lock()
	defer s.peerHeadsMu.Unlock()
	old := s.peerHeads[pid]
	if old == head {
		return old, false
	}
	s.peerHeads[pid] = head
	return old, true
}

func (s *Syncer) saveSyncStatus() {
	if s.syncStatusPath == "" {
		return
	}

	s.lastSyncErrMu.RLock()
	le := s.lastSyncError
	s.lastSyncErrMu.RUnlock()

	s.peerHeadsMu.RLock()
	peerHeads := make(map[string]string, len(s.peerHeads))
	for k, v := range s.peerHeads {
		peerHeads[k.String()] = v.String()
	}
	s.peerHeadsMu.RUnlock()

	status := SyncStatus{
		LastSyncError: le.Message,
		LastErrorTime: le.Time,
		PeerHeads:     peerHeads,
	}

	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		logger.WarnCF("sync", "failed to marshal sync status", map[string]any{"error": err.Error()})
		return
	}

	if err := fileutil.WriteFileAtomic(s.syncStatusPath, data, 0o644); err != nil {
		logger.WarnCF("sync", "failed to save sync status", map[string]any{"error": err.Error()})
	}
}

// LoadSyncStatus reads the persisted sync status for a workspace.
func LoadSyncStatus(workspace string) (SyncStatus, error) {
	path := filepath.Join(filepath.Dir(workspace), "sync-status.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SyncStatus{}, nil
		}
		return SyncStatus{}, err
	}
	var status SyncStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return SyncStatus{}, err
	}
	return status, nil
}
