package mesh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const (
	// maxStoredTasks bounds the task store so a peer cannot exhaust memory by
	// submitting unbounded tasks.
	maxStoredTasks = 256
	// taskResultTTL is how long a completed task's result is kept for
	// retrieval before it may be evicted.
	taskResultTTL = time.Hour
	// taskSaveCoalesce is how long to wait after a mutation before writing the
	// task store to disk. Multiple rapid mutations are coalesced into one write.
	taskSaveCoalesce = time.Second
)

// MeshTask is a single remote task owned by the callee.
type MeshTask struct {
	ID         string
	CorrID     string
	Owner      peer.ID
	AgentID    string
	Model      string
	Status     agenttask.TaskStatus
	Result     *toolshared.ToolResult
	Err        string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	cancelFunc context.CancelFunc
	done       chan struct{}
}

// taskRecord is the on-disk, JSON-serializable form of MeshTask. It omits
// in-memory channels and cancel functions.
type taskRecord struct {
	ID        string                 `json:"id"`
	CorrID    string                 `json:"corr_id,omitempty"`
	Owner     string                 `json:"owner"`
	AgentID   string                 `json:"agent_id"`
	Model     string                 `json:"model,omitempty"`
	Status    agenttask.TaskStatus   `json:"status"`
	Result    *toolshared.ToolResult `json:"result,omitempty"`
	Err       string                 `json:"err,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// TaskStore tracks remote tasks submitted to this node. Tasks are keyed by
// task id and owned by the submitting peer; only the owner may query, fetch
// results for, or cancel a task. When configured with a path, the store is
// persisted as a JSONL file and reloaded on startup.
type TaskStore struct {
	mu     sync.Mutex
	tasks  map[string]*MeshTask
	byCorr map[peer.ID]map[string]string // owner -> correlation id -> task id
	closed bool

	path      string
	saveMu    sync.Mutex
	saveErr   error
	saveTimer *time.Timer

	// onEvict is invoked when a non-terminal task is evicted to make room.
	// It is called with s.mu held, so the callback must not re-enter the
	// store.
	onEvict func(*MeshTask)
}

// NewTaskStore creates an empty in-memory task store.
func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks:  make(map[string]*MeshTask),
		byCorr: make(map[peer.ID]map[string]string),
	}
}

// NewTaskStoreWithPath creates a task store backed by a JSONL file. Callers
// should invoke Load before use to recover tasks from a previous run.
func NewTaskStoreWithPath(path string) *TaskStore {
	s := NewTaskStore()
	s.path = path
	return s
}

// SetPath enables persistence at the given path. The next mutation or an
// explicit Load will use it. It is safe for tests and the daemon to set this
// before Mesh.Start.
func (s *TaskStore) SetPath(path string) {
	s.saveMu.Lock()
	s.path = path
	s.saveMu.Unlock()
}

// Load reads the persisted task file and returns any tasks that were still
// running when the file was last written. Those tasks are marked as errors
// (the original goroutines cannot be resumed) and their done channels are
// closed so waiters return immediately.
func (s *TaskStore) Load() ([]*MeshTask, error) {
	s.saveMu.Lock()
	path := s.path
	s.saveMu.Unlock()
	if path == "" {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open task store: %w", err)
	}
	defer f.Close()

	var restarted []*MeshTask
	s.mu.Lock()
	defer s.mu.Unlock()

	dec := json.NewDecoder(f)
	for {
		var rec taskRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode task record: %w", err)
		}
		owner, err := peer.Decode(rec.Owner)
		if err != nil {
			// Tolerate a stale/corrupt record: log and continue.
			continue
		}
		t := &MeshTask{
			ID:        rec.ID,
			CorrID:    rec.CorrID,
			Owner:     owner,
			AgentID:   rec.AgentID,
			Model:     rec.Model,
			Status:    rec.Status,
			Result:    rec.Result,
			Err:       rec.Err,
			CreatedAt: rec.CreatedAt,
			UpdatedAt: rec.UpdatedAt,
			done:      make(chan struct{}),
		}
		if !t.Status.Terminal() {
			t.Status = agenttask.StatusError
			t.Err = "daemon restarted"
			t.UpdatedAt = time.Now().UTC()
			close(t.done)
			restarted = append(restarted, t)
		} else {
			close(t.done)
		}
		s.tasks[t.ID] = t
		if t.CorrID != "" {
			if _, ok := s.byCorr[t.Owner]; !ok {
				s.byCorr[t.Owner] = make(map[string]string)
			}
			s.byCorr[t.Owner][t.CorrID] = t.ID
		}
	}

	return restarted, nil
}

// save writes the current task set to the configured path atomically.
func (s *TaskStore) save() error {
	s.saveMu.Lock()
	path := s.path
	s.saveMu.Unlock()
	if path == "" {
		return nil
	}

	// Snapshot the current tasks.
	s.mu.Lock()
	records := make([]taskRecord, 0, len(s.tasks))
	for _, t := range s.tasks {
		records = append(records, taskRecord{
			ID:        t.ID,
			CorrID:    t.CorrID,
			Owner:     t.Owner.String(),
			AgentID:   t.AgentID,
			Model:     t.Model,
			Status:    t.Status,
			Result:    t.Result,
			Err:       t.Err,
			CreatedAt: t.CreatedAt,
			UpdatedAt: t.UpdatedAt,
		})
	}
	s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir task store: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create task store temp: %w", err)
	}
	tmp := f.Name()
	enc := json.NewEncoder(f)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("encode task record: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync task store: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close task store temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename task store: %w", err)
	}
	return nil
}

// SetOnEvict registers a callback invoked when a non-terminal task is
// evicted to make room. It must be called before the store accepts submissions.
func (s *TaskStore) SetOnEvict(fn func(*MeshTask)) {
	s.mu.Lock()
	s.onEvict = fn
	s.mu.Unlock()
}

// scheduleSave coalesces writes; multiple rapid mutations result in one disk
// write about a second after activity stops.
func (s *TaskStore) scheduleSave() {
	s.saveMu.Lock()
	if s.path == "" {
		s.saveMu.Unlock()
		return
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(taskSaveCoalesce, func() {
		s.saveMu.Lock()
		s.saveTimer = nil
		s.saveMu.Unlock()
		if err := s.save(); err != nil {
			s.saveMu.Lock()
			s.saveErr = err
			s.saveMu.Unlock()
		}
	})
	s.saveMu.Unlock()
}

// flushSave cancels any pending coalesced write and runs save synchronously.
// It is used during shutdown.
func (s *TaskStore) flushSave() {
	s.saveMu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveMu.Unlock()
	if err := s.save(); err != nil {
		s.saveMu.Lock()
		s.saveErr = err
		s.saveMu.Unlock()
	}
}

// SaveError returns the most recent persistence error, if any.
func (s *TaskStore) SaveError() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.saveErr
}

func newTaskID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("task-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b[:]))
}

// Submit registers a new task. If the same peer resubmits the same
// correlation id, the existing task is returned with created=false.
func (s *TaskStore) Submit(owner peer.ID, req agenttask.Request) (task *MeshTask, created bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, false, fmt.Errorf("task store is closed")
	}

	if req.CorrelationID != "" {
		if byID, ok := s.byCorr[owner]; ok {
			if id, ok := byID[req.CorrelationID]; ok {
				if existing, ok := s.tasks[id]; ok {
					return existing, false, nil
				}
			}
		}
	}

	s.evictLocked()

	task = &MeshTask{
		ID:        newTaskID(),
		CorrID:    req.CorrelationID,
		Owner:     owner,
		AgentID:   req.TargetAgentID,
		Model:     req.Model,
		Status:    agenttask.StatusAccepted,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		done:      make(chan struct{}),
	}
	s.tasks[task.ID] = task
	if req.CorrelationID != "" {
		if _, ok := s.byCorr[owner]; !ok {
			s.byCorr[owner] = make(map[string]string)
		}
		s.byCorr[owner][req.CorrelationID] = task.ID
	}

	s.scheduleSave()
	return task, true, nil
}

// evictLocked drops terminal tasks past the TTL and, when the store is full,
// the oldest terminal tasks first. It must be called with s.mu held.
func (s *TaskStore) evictLocked() {
	now := time.Now()
	for id, t := range s.tasks {
		if t.Status.Terminal() && now.Sub(t.UpdatedAt) > taskResultTTL {
			s.deleteLocked(id)
		}
	}
	for len(s.tasks) >= maxStoredTasks {
		// Prefer evicting the oldest terminal task; fall back to the oldest
		// task overall so the store never grows without bound.
		var oldest *MeshTask
		var oldestTerminal *MeshTask
		for _, t := range s.tasks {
			if oldest == nil || t.CreatedAt.Before(oldest.CreatedAt) {
				oldest = t
			}
			if t.Status.Terminal() &&
				(oldestTerminal == nil || t.UpdatedAt.Before(oldestTerminal.UpdatedAt)) {
				oldestTerminal = t
			}
		}
		victim := oldestTerminal
		if victim == nil {
			victim = oldest
		}
		if victim == nil {
			return
		}
		if !victim.Status.Terminal() {
			// The victim is still running. Mark it as errored and close its
			// done channel so any waiter returns immediately, then notify the
			// mesh layer so it can publish a terminal event. The task goroutine
			// will eventually call Finish, which is a no-op once the task is
			// gone from the store.
			if victim.cancelFunc != nil {
				victim.cancelFunc()
			}
			victim.Status = agenttask.StatusError
			victim.Err = "evicted to make room for new tasks"
			victim.UpdatedAt = time.Now().UTC()
			select {
			case <-victim.done:
			default:
				close(victim.done)
			}
			if s.onEvict != nil {
				s.onEvict(victim)
			}
		}
		s.deleteLocked(victim.ID)
	}
}

func (s *TaskStore) deleteLocked(id string) {
	t, ok := s.tasks[id]
	if !ok {
		return
	}
	delete(s.tasks, id)
	if byID, ok := s.byCorr[t.Owner]; ok {
		if t.CorrID != "" && byID[t.CorrID] == id {
			delete(byID, t.CorrID)
		}
		if len(byID) == 0 {
			delete(s.byCorr, t.Owner)
		}
	}
}

// getOwned returns the task only if it belongs to the given peer.
func (s *TaskStore) getOwned(id string, owner peer.ID) (*MeshTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return nil, false
	}
	return t, true
}

// Start marks an accepted task as running and registers its cancel func.
func (s *TaskStore) Start(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok && t.Status == agenttask.StatusAccepted {
		t.Status = agenttask.StatusRunning
		t.UpdatedAt = time.Now().UTC()
		t.cancelFunc = cancel
		s.scheduleSave()
	}
}

// Finish records the terminal status and result of a task.
func (s *TaskStore) Finish(id string, status agenttask.TaskStatus, result *toolshared.ToolResult, taskErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok && !t.Status.Terminal() {
		t.Status = status
		t.Result = result
		t.Err = taskErr
		t.UpdatedAt = time.Now().UTC()
		close(t.done)
		s.scheduleSave()
	}
}

// Cancel marks a task cancelled and invokes its cancel func. Only the owner
// may cancel. Returns false if the task does not exist or is not owned.
func (s *TaskStore) Cancel(id string, owner peer.ID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.Owner != owner {
		return false
	}
	if !t.Status.Terminal() {
		t.Status = agenttask.StatusCancelled
		t.Err = "cancelled by owner"
		t.UpdatedAt = time.Now().UTC()
		if t.cancelFunc != nil {
			t.cancelFunc()
		}
		select {
		case <-t.done:
		default:
			close(t.done)
		}
		s.scheduleSave()
	}
	return true
}

// Wait blocks until the task reaches a terminal state, the wait duration
// elapses, or ctx is canceled. Returns the task (possibly still running).
func (s *TaskStore) Wait(ctx context.Context, id string, owner peer.ID, wait time.Duration) (*MeshTask, bool) {
	t, ok := s.getOwned(id, owner)
	if !ok {
		return nil, false
	}
	if t.Status.Terminal() || wait <= 0 {
		return t, true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-t.done:
	}
	return t, true
}

// ActiveCount returns the number of non-terminal tasks across all owners —
// the node's current remote-task load, advertised in capability manifests.
func (s *TaskStore) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, t := range s.tasks {
		if !t.Status.Terminal() {
			n++
		}
	}
	return n
}

// List returns info for every task owned by the given peer.
func (s *TaskStore) List(owner peer.ID) []agenttask.TaskInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agenttask.TaskInfo, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.Owner != owner {
			continue
		}
		out = append(out, t.Info())
	}
	return out
}

// ListAll returns a snapshot of every stored task. It is intended for
// persistence and event replay, not routine querying.
func (s *TaskStore) ListAll() []*MeshTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*MeshTask, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	return out
}

// Info snapshots the public view of a task.
func (t *MeshTask) Info() agenttask.TaskInfo {
	return agenttask.TaskInfo{
		TaskID:    t.ID,
		Status:    t.Status,
		AgentID:   t.AgentID,
		Model:     t.Model,
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
		Error:     t.Err,
	}
}

// Close cancels every non-terminal task and prevents new submissions.
func (s *TaskStore) Close() {
	s.mu.Lock()
	s.closed = true
	var toClose []*MeshTask
	for _, t := range s.tasks {
		if !t.Status.Terminal() {
			t.Status = agenttask.StatusCancelled
			t.Err = "mesh stopped"
			t.UpdatedAt = time.Now().UTC()
			if t.cancelFunc != nil {
				t.cancelFunc()
			}
			toClose = append(toClose, t)
		}
	}
	s.mu.Unlock()
	for _, t := range toClose {
		select {
		case <-t.done:
		default:
			close(t.done)
		}
	}
	s.flushSave()
}
