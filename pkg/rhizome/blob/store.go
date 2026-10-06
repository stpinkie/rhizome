package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/logger"
)

// Store is a content-addressed on-disk blob store. Blobs live at
// <dir>/<sha256> with a JSON metadata sidecar at <dir>/<sha256>.json.
// A background reaper removes blobs older than the configured TTL.
type Store struct {
	dir      string
	maxBytes int64
	ttl      time.Duration

	stop   chan struct{}
	wg     sync.WaitGroup
	once   sync.Once
	stopDo sync.Once
}

// NewStore creates a blob store rooted at dir. maxBytes bounds a single
// blob; ttl bounds how long a blob is kept (0 = keep forever).
func NewStore(dir string, maxBytes int64, ttl time.Duration) *Store {
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	return &Store{
		dir:      dir,
		maxBytes: maxBytes,
		ttl:      ttl,
		stop:     make(chan struct{}),
	}
}

// MaxBytes returns the configured per-blob size cap.
func (s *Store) MaxBytes() int64 { return s.maxBytes }

// Dir returns the store root directory.
func (s *Store) Dir() string { return s.dir }

// blobPath returns the content path for a hash. Callers must have validated
// the hash via ValidateHash so it cannot escape the store directory.
func (s *Store) blobPath(h string) string {
	return filepath.Join(s.dir, h)
}

func (s *Store) metaPath(h string) string {
	return filepath.Join(s.dir, h+".json")
}

// Has reports whether a blob with the given hash exists locally.
func (s *Store) Has(h string) bool {
	if ValidateHash(h) != nil {
		return false
	}
	info, err := os.Stat(s.blobPath(h))
	return err == nil && info.Mode().IsRegular()
}

// Path returns the on-disk path for a stored blob, or an error.
func (s *Store) Path(h string) (string, error) {
	if err := ValidateHash(h); err != nil {
		return "", err
	}
	p := s.blobPath(h)
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("blob %s not found", h)
	}
	return p, nil
}

// Stat returns the metadata for a stored blob.
func (s *Store) Stat(h string) (Meta, error) {
	if err := ValidateHash(h); err != nil {
		return Meta{}, err
	}
	info, err := os.Stat(s.blobPath(h))
	if err != nil || !info.Mode().IsRegular() {
		return Meta{}, fmt.Errorf("blob %s not found", h)
	}
	meta := Meta{Size: info.Size()}
	if data, err := os.ReadFile(s.metaPath(h)); err == nil {
		var m Meta
		if json.Unmarshal(data, &m) == nil {
			m.Size = info.Size()
			meta = m
		}
	}
	return meta, nil
}

// Open returns a reader for a stored blob along with its metadata.
func (s *Store) Open(h string) (io.ReadCloser, Meta, error) {
	meta, err := s.Stat(h)
	if err != nil {
		return nil, Meta{}, err
	}
	f, err := os.Open(s.blobPath(h))
	if err != nil {
		return nil, Meta{}, err
	}
	return f, meta, nil
}

// Put streams content into the store and returns its SHA-256 hash. The
// content is hashed while writing; when wantHash is non-empty the computed
// digest must match it, so a peer can announce a hash up front and have the
// store reject corrupted content on commit.
func (s *Store) Put(r io.Reader, meta Meta, wantHash string) (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	if wantHash != "" {
		if err := ValidateHash(wantHash); err != nil {
			return "", err
		}
	}

	tmp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	hw := sha256.New()
	w := io.MultiWriter(tmp, hw)
	n, err := io.Copy(w, io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("write blob: %w", err)
	}
	if n > s.maxBytes {
		return "", fmt.Errorf("blob exceeds max size %d bytes", s.maxBytes)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	sum := hex.EncodeToString(hw.Sum(nil))
	if wantHash != "" && sum != wantHash {
		return "", fmt.Errorf("blob hash mismatch: announced %s, received %s", wantHash, sum)
	}

	meta.Size = n
	if meta.StoredAt == 0 {
		meta.StoredAt = time.Now().Unix()
	}
	if len(meta.Name) > maxNameLen {
		meta.Name = meta.Name[:maxNameLen]
	}
	if len(meta.ContentType) > maxNameLen {
		meta.ContentType = meta.ContentType[:maxNameLen]
	}

	dest := s.blobPath(sum)
	if _, err := os.Stat(dest); err != nil {
		if err := os.Rename(tmpPath, dest); err != nil {
			return "", fmt.Errorf("commit blob: %w", err)
		}
	} else {
		// Content-addressed: identical content already stored; drop the
		// duplicate and just refresh the metadata sidecar.
		_ = os.Remove(tmpPath)
	}
	committed = true

	if data, err := json.Marshal(meta); err == nil {
		if err := os.WriteFile(s.metaPath(sum), data, 0o600); err != nil {
			logger.WarnCF("blob", "failed to write blob metadata", map[string]any{
				"hash":  sum,
				"error": err.Error(),
			})
		}
	}
	return sum, nil
}

// PutFile stores an existing local file.
func (s *Store) PutFile(path string, meta Meta) (string, error) {
	//nolint:gosec // G304: PutFile opens the caller-supplied path by design.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() > s.maxBytes {
		return "", fmt.Errorf("file exceeds max blob size %d bytes", s.maxBytes)
	}
	if meta.Name == "" {
		meta.Name = filepath.Base(path)
	}
	return s.Put(f, meta, "")
}

// Writer creates a streaming sink for a blob whose announced hash and size
// are known up front — used by the PUT server path so content is verified
// while it is received. The caller must Close the writer; committing happens
// on Close.
type blobWriter struct {
	store *Store
	tmp   *os.File
	hw    hash.Hash
	path  string
	want  string
	limit int64
	// n counts total bytes staged (a resumed prefix plus fresh writes) so
	// the limit and Complete checks see the whole blob.
	n    int64
	done bool
	err  error
}

// NewResumableWriter returns a blob writer staged at a deterministic
// .partial-<hash> path so a dropped transfer can resume where it left
// off. An existing partial is re-hashed into the running digest (keeping
// the end-state hash check authoritative) and appended to; Staged reports
// how many bytes were already on disk. Suspend keeps the partial for a
// later attempt; Abort and a failed Complete delete it.
func (s *Store) NewResumableWriter(wantHash string, limit int64) (*blobWriter, error) {
	if err := ValidateHash(wantHash); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > s.maxBytes {
		return nil, fmt.Errorf("blob size %d out of bounds (max %d)", limit, s.maxBytes)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	path := s.partialPath(wantHash)
	// O_RDWR: an existing partial is read back to seed the digest before
	// new writes append to it — O_WRONLY cannot serve that read.
	//nolint:gosec // G304: path is the hash-derived partial under the store root.
	tmp, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	w := &blobWriter{
		store: s,
		tmp:   tmp,
		hw:    sha256.New(),
		path:  path,
		want:  wantHash,
		limit: limit,
	}
	// Seed the digest and counter with whatever an earlier attempt staged
	// so the final hash still covers the whole blob.
	if info, err := tmp.Stat(); err == nil && info.Size() > 0 {
		if info.Size() <= limit {
			if _, err := tmp.Seek(0, io.SeekStart); err == nil {
				if _, err := io.Copy(w.hw, tmp); err == nil {
					w.n = info.Size()
				}
			}
			_, _ = tmp.Seek(0, io.SeekEnd)
		}
		if w.n == 0 {
			// Oversized or unreadable partial — restart it.
			_ = tmp.Truncate(0)
			_, _ = tmp.Seek(0, io.SeekStart)
		}
	}
	return w, nil
}

// partialPath is the deterministic staging path for a resumable blob.
func (s *Store) partialPath(h string) string {
	return filepath.Join(s.dir, ".partial-"+h)
}

// PartialSize reports how many bytes are staged for a resumable transfer.
func (s *Store) PartialSize(h string) int64 {
	if ValidateHash(h) != nil {
		return 0
	}
	info, err := os.Stat(s.partialPath(h))
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

// NewBlobWriter returns a writer that streams content to a temp file.
// Call Write for each chunk (bounded by limit bytes total), then Close to
// verify the hash and commit. Close returns the verified hash.
func (s *Store) NewBlobWriter(wantHash string, limit int64) (*blobWriter, error) {
	if err := ValidateHash(wantHash); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > s.maxBytes {
		return nil, fmt.Errorf("blob size %d out of bounds (max %d)", limit, s.maxBytes)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(s.dir, ".incoming-*")
	if err != nil {
		return nil, err
	}
	return &blobWriter{
		store: s,
		tmp:   tmp,
		hw:    sha256.New(),
		path:  tmp.Name(),
		want:  wantHash,
		limit: limit,
	}, nil
}

// Write appends a chunk. Writes beyond the announced size are rejected.
func (w *blobWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.done {
		return 0, fmt.Errorf("blob writer closed")
	}
	if w.n+int64(len(p)) > w.limit {
		w.err = fmt.Errorf("blob content exceeds announced size")
		return 0, w.err
	}
	n, err := w.tmp.Write(p)
	w.n += int64(n)
	if _, herr := w.hw.Write(p[:n]); herr != nil {
		w.err = herr
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

// closeTmp closes the temp file handle if still open.
func (w *blobWriter) closeTmp() {
	if w.tmp != nil {
		_ = w.tmp.Close()
		w.tmp = nil
	}
}

// Staged reports bytes held so far — a resumed prefix plus fresh writes.
func (w *blobWriter) Staged() int64 { return w.n }

// Suspend ends this attempt without discarding the staged content. For a
// resumable (.partial-<hash>) writer the file stays for the next attempt;
// for a random .incoming-* writer there is no way to re-find it, so the
// file is removed like Abort.
func (w *blobWriter) Suspend() {
	if w.done {
		return
	}
	w.done = true
	w.closeTmp()
	if filepath.Base(w.path) != ".partial-"+w.want {
		_ = os.Remove(w.path)
	}
}

// Abort discards the in-progress blob.
func (w *blobWriter) Abort() {
	if w.done {
		return
	}
	w.done = true
	w.closeTmp()
	_ = os.Remove(w.path)
}

// discard is the internal cleanup used by Complete after it has already
// marked the writer done — it must not go through Abort's done check.
func (w *blobWriter) discard() {
	w.done = true
	w.closeTmp()
	_ = os.Remove(w.path)
}

// Complete verifies the received content: the byte count must equal the
// announced size and the SHA-256 must match the announced hash. On success
// the blob is committed to the store and the hash is returned.
func (w *blobWriter) Complete() (string, error) {
	if w.done {
		return "", fmt.Errorf("blob writer already finished")
	}
	w.done = true
	if w.err != nil {
		w.discard()
		return "", w.err
	}
	if w.n != w.limit {
		w.discard()
		return "", fmt.Errorf("blob truncated: got %d of %d bytes", w.n, w.limit)
	}
	w.closeTmp()

	sum := hex.EncodeToString(w.hw.Sum(nil))
	if sum != w.want {
		_ = os.Remove(w.path)
		return "", fmt.Errorf("blob hash mismatch: announced %s, received %s", w.want, sum)
	}

	dest := w.store.blobPath(sum)
	if _, err := os.Stat(dest); err != nil {
		if err := os.Rename(w.path, dest); err != nil {
			return "", fmt.Errorf("commit blob: %w", err)
		}
	} else {
		_ = os.Remove(w.path)
	}
	return sum, nil
}

// WriteMeta persists the metadata sidecar for a committed blob.
func (s *Store) WriteMeta(h string, meta Meta) error {
	if err := ValidateHash(h); err != nil {
		return err
	}
	info, err := os.Stat(s.blobPath(h))
	if err != nil {
		return err
	}
	meta.Size = info.Size()
	if meta.StoredAt == 0 {
		meta.StoredAt = time.Now().Unix()
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(h), data, 0o600)
}

// Entry describes one stored blob for operator listing.
type Entry struct {
	Hash      string `json:"hash"`
	Name      string `json:"name,omitempty"`
	Size      int64  `json:"size"`
	Owner     string `json:"owner,omitempty"`
	StoredAt  int64  `json:"stored_at,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Partial   int64  `json:"partial,omitempty"`
}

// List enumerates committed blobs in the store along with their TTL state
// (expires_at is omitted when the store has no TTL). In-flight resumable
// transfers show as partial byte counts.
func (s *Store) List() ([]Entry, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if len(name) > 9 && name[:9] == ".partial-" {
			hash := name[9:]
			if ValidateHash(hash) == nil {
				if info, statErr := e.Info(); statErr == nil {
					out = append(out, Entry{Hash: hash, Partial: info.Size()})
				}
			}
			continue
		}
		if len(name) != 64 || ValidateHash(name) != nil {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		ent := Entry{Hash: name, Size: info.Size()}
		if meta, err := s.Stat(name); err == nil {
			ent.Name = meta.Name
			ent.Owner = meta.Owner
			ent.StoredAt = meta.StoredAt
		}
		if ent.StoredAt == 0 {
			ent.StoredAt = info.ModTime().Unix()
		}
		if s.ttl > 0 {
			ent.ExpiresAt = ent.StoredAt + int64(s.ttl.Seconds())
		}
		out = append(out, ent)
	}
	return out, nil
}

// Start begins the TTL reaper goroutine. Safe to call once.
func (s *Store) Start(ctx context.Context) {
	s.once.Do(func() {
		if s.ttl <= 0 {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-s.stop:
					return
				case <-ticker.C:
					s.reap()
				}
			}
		}()
	})
}

// Stop terminates the reaper.
func (s *Store) Stop() {
	s.stopDo.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// reap removes blobs whose stored-at time is older than the TTL.
func (s *Store) reap() {
	if s.ttl <= 0 {
		return
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-s.ttl).Unix()
	for _, e := range entries {
		name := e.Name()
		if len(name) != 64 {
			continue // meta sidecars (.json) and temp files handled below
		}
		if ValidateHash(name) != nil {
			continue
		}
		meta, err := s.Stat(name)
		storedAt := meta.StoredAt
		if err != nil || storedAt == 0 {
			if info, statErr := e.Info(); statErr == nil {
				storedAt = info.ModTime().Unix()
			}
		}
		if storedAt > 0 && storedAt < cutoff {
			_ = os.Remove(s.blobPath(name))
			_ = os.Remove(s.metaPath(name))
		}
	}
	// Sweep orphaned sidecars and stale temp files.
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".json" {
			base := name[:len(name)-len(".json")]
			if ValidateHash(base) == nil && !s.Has(base) {
				_ = os.Remove(s.metaPath(base))
			}
			continue
		}
		if len(name) > 10 && (name[:10] == ".incoming-" || name[:9] == ".partial-") {
			if info, statErr := e.Info(); statErr == nil && info.ModTime().Unix() < cutoff {
				_ = os.Remove(filepath.Join(s.dir, name))
			}
		}
	}
}
