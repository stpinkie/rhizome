package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/merge"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

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

	pulling   map[peer.ID]bool
	pullingMu sync.Mutex

	autoSync         bool
	commitInterval   time.Duration
	announceInterval time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex

	watcher *Watcher
	ticker  *time.Ticker
	stop    chan struct{}
	wg      sync.WaitGroup
}

// Config controls syncer behaviour.
type Config struct {
	Workspace        string
	NodeName         string
	Node             *network.Node
	AutoSync         bool
	CommitInterval   time.Duration
	AnnounceInterval time.Duration
	Exclude          []string
}

// NewSyncer opens the workspace repository and creates a syncer.
func NewSyncer(ctx context.Context, cfg Config) (*Syncer, error) {
	repo, w, err := OpenOrInit(cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("open workspace repo: %w", err)
	}

	s := &Syncer{
		repo:             repo,
		worktree:         w,
		node:             cfg.Node,
		nodeName:         cfg.NodeName,
		exclude:          cfg.Exclude,
		peerHeads:        make(map[peer.ID]plumbing.Hash),
		pulling:          make(map[peer.ID]bool),
		autoSync:         cfg.AutoSync,
		commitInterval:   cfg.CommitInterval,
		announceInterval: cfg.AnnounceInterval,
		stop:             make(chan struct{}),
	}

	s.transport = NewTransport(cfg.Node.Host(), s)

	return s, nil
}

// Start begins file watching, the transport listener, and anti-entropy.
func (s *Syncer) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.transport.Start(s.ctx)
	}()

	// Wait until the stream handler is registered before returning.
	select {
	case <-s.transport.Ready():
	case <-time.After(5 * time.Second):
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
				s.commitAndAnnounce(s.ctx)
			}()
		})
		if err != nil {
			if s.cancel != nil {
				s.cancel()
			}
			return fmt.Errorf("start watcher: %w", err)
		}
	}

	if s.announceInterval > 0 {
		s.ticker = time.NewTicker(s.announceInterval)
		s.wg.Add(1)
		go s.antiEntropyLoop(s.ctx)
	}

	return nil
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
	s.wg.Wait()
	return nil
}

// PullFrom fetches and merges from a single peer.
func (s *Syncer) PullFrom(ctx context.Context, pid peer.ID) error {
	s.pullingMu.Lock()
	if s.pulling[pid] {
		s.pullingMu.Unlock()
		return nil
	}
	s.pulling[pid] = true
	s.pullingMu.Unlock()

	defer func() {
		s.pullingMu.Lock()
		s.pulling[pid] = false
		s.pullingMu.Unlock()
	}()

	// Ensure the peer is actually connected before opening a sync stream.
	if !s.waitForPeerConnection(ctx, pid, 5*time.Second) {
		return fmt.Errorf("peer %s is not connected", pid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.pullFromLocked(ctx, pid)
}

func (s *Syncer) waitForPeerConnection(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range s.node.ConnectedPeers() {
			if p == pid {
				return true
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
	head, err := Head(s.repo)
	if err != nil {
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
			return fmt.Errorf("fetch from %s: %w", pid, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	s.peerHeadsMu.Lock()
	s.peerHeads[pid] = remoteHead
	s.peerHeadsMu.Unlock()

	if err := s.applyPackfileAndMergeLocked(ctx, pack, remoteHead); err != nil {
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
		return plumbing.ZeroHash, err
	}
	return head, s.transport.AnnounceHead(ctx, pid, head)
}

// commitAndAnnounce commits pending changes and announces the new HEAD.
func (s *Syncer) commitAndAnnounce(ctx context.Context) (plumbing.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitAndAnnounceLocked(ctx)
}

func (s *Syncer) commitAndAnnounceLocked(ctx context.Context) (plumbing.Hash, error) {
	dirty, err := HasUncommitted(s.worktree)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("status: %w", err)
	}

	var head plumbing.Hash
	if dirty {
		head, err = Commit(s.worktree, s.nodeName, fmt.Sprintf("%s: workspace sync", s.nodeName))
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
			_ = s.transport.AnnounceHead(ctx, p, head)
		}(pid)
	}

	return head, nil
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
	s.peerHeadsMu.Lock()
	old := s.peerHeads[from]
	s.peerHeads[from] = head
	s.peerHeadsMu.Unlock()

	if old == head {
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
			_ = s.PullFrom(ctx, p)
		}(pid)
	}
}

// applyPackfileAndMergeLocked decodes a packfile and fast-forwards or merges.
// It must be called with s.mu held.
func (s *Syncer) applyPackfileAndMergeLocked(ctx context.Context, pack []byte, remoteHead plumbing.Hash) error {
	if _, err := s.commitAndAnnounceLocked(ctx); err != nil {
		// Commit our local work before merging. If it fails we still try to apply.
		_ = err
	}

	if err := applyPackfile(s.repo.Storer, pack); err != nil {
		return fmt.Errorf("apply packfile: %w", err)
	}

	ours, err := Head(s.repo)
	if err != nil {
		return err
	}

	if ours == remoteHead || remoteHead.IsZero() {
		return nil
	}

	oCommit, err := s.repo.CommitObject(ours)
	if err != nil {
		return fmt.Errorf("load ours: %w", err)
	}
	tCommit, err := s.repo.CommitObject(remoteHead)
	if err != nil {
		return fmt.Errorf("load theirs: %w", err)
	}

	// Fast-forward if possible.
	isAncestor, err := oCommit.IsAncestor(tCommit)
	if err != nil {
		return fmt.Errorf("isAncestor: %w", err)
	}
	if isAncestor {
		return s.fastForwardToLocked(remoteHead)
	}

	// Nothing to do if they are behind us.
	isDescendant, err := tCommit.IsAncestor(oCommit)
	if err != nil {
		return fmt.Errorf("isDescendant: %w", err)
	}
	if isDescendant {
		return nil
	}

	// Three-way merge.
	bases, err := oCommit.MergeBase(tCommit)
	if err != nil {
		return fmt.Errorf("merge base: %w", err)
	}
	if len(bases) == 0 {
		return fmt.Errorf("no merge base with %s", remoteHead)
	}
	baseCommit := bases[0]

	mergedTree, conflicts, err := merge.MergeTrees(s.repo.Storer, baseCommit.TreeHash, oCommit.TreeHash, tCommit.TreeHash)
	if err != nil {
		return fmt.Errorf("merge trees: %w", err)
	}

	mergeHash, err := s.createMergeCommitLocked(ours, remoteHead, mergedTree, conflicts)
	if err != nil {
		return fmt.Errorf("create merge commit: %w", err)
	}

	if err := s.fastForwardToLocked(mergeHash); err != nil {
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
		return err
	}
	if err := s.worktree.Reset(&git.ResetOptions{Commit: target, Mode: git.HardReset}); err != nil {
		return err
	}
	return nil
}

func (s *Syncer) createMergeCommitLocked(ours, theirs plumbing.Hash, tree plumbing.Hash, conflicts []string) (plumbing.Hash, error) {
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
