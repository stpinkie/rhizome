package swarm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// maxStoredOffers bounds persisted offers so the file stays small.
const maxStoredOffers = 256

// offerRecord is the persisted view of one published, non-terminal offer.
type offerRecord struct {
	Info    OfferInfo `json:"info"`
	Retries int       `json:"retries"`
}

// offerStore persists published non-terminal offers as a bounded JSONL file
// at <RHIZOME_HOME>/swarm-offers.jsonl so Start can re-drive work that was
// in flight when the daemon stopped.
type offerStore struct {
	mu   sync.Mutex
	path string
}

func newOfferStore(path string) *offerStore {
	return &offerStore{path: path}
}

// Save snapshots the given records atomically (tmp-write + rename).
func (st *offerStore) Save(records []offerRecord) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.save(records)
}

// Load reads the persisted offer file; missing/corrupt files are tolerated.
func (st *offerStore) Load() ([]offerRecord, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.path == "" {
		return nil, nil
	}
	f, err := os.Open(st.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open offer store: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []offerRecord
	dec := json.NewDecoder(f)
	for {
		var rec offerRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode offer record: %w", err)
		}
		if rec.Info.Status.Terminal() {
			continue // stale terminal entry — nothing to re-drive
		}
		records = append(records, rec)
	}
	if len(records) > maxStoredOffers {
		records = records[len(records)-maxStoredOffers:]
	}
	return records, nil
}

func (st *offerStore) save(records []offerRecord) {
	saveJSONL(st.path, records)
}
