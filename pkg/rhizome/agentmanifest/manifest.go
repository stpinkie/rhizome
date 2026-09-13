// Package agentmanifest implements AIEOS-style agent identity manifests:
// signed documents describing an agent's identity, model, and skills, issued
// by the hosting node's Ed25519 identity. Manifests are embedded in mesh
// capability announcements and persisted to the synced workspace under
// agents/<id>.manifest.json so swarm members share a verifiable identity
// layer without a new wire protocol.
package agentmanifest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Manifest is a signed AIEOS-style identity document for one agent.
type Manifest struct {
	// AgentID is the configured agent id (e.g. "main").
	AgentID string `json:"agent_id"`
	// Name is the agent's display name from config.
	Name string `json:"name,omitempty"`
	// Persona is a short identity descriptor; when the agent workspace has an
	// IDENTITY.md, this carries its sha256 fingerprint (prefixed "sha256:").
	Persona string `json:"persona,omitempty"`
	// Models lists the agent's configured model names (primary first).
	Models []string `json:"models,omitempty"`
	// Skills lists the agent's configured skill names.
	Skills []string `json:"skills,omitempty"`
	// PeerID is the issuing node's libp2p peer id.
	PeerID string `json:"peer_id"`
	// IssuedAt is when the manifest was issued.
	IssuedAt time.Time `json:"issued_at"`
	// Signature is the Ed25519 signature over the canonical JSON encoding of
	// all fields above.
	Signature []byte `json:"signature,omitempty"`
}

// Input carries the unsigned fields a node stamps into a manifest.
type Input struct {
	AgentID string
	Name    string
	Persona string
	Models  []string
	Skills  []string
}

// agentIDPattern bounds manifest file names.
var agentIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ValidAgentID reports whether id is usable in a manifest file name.
func ValidAgentID(id string) bool {
	return agentIDPattern.MatchString(id)
}

// canonical returns the signed JSON encoding (all fields except Signature).
func (m *Manifest) canonical() ([]byte, error) {
	sig := m.Signature
	m.Signature = nil
	defer func() { m.Signature = sig }()
	return json.Marshal(m)
}

// Sign stamps PeerID and IssuedAt (when unset) and signs the manifest.
func (m *Manifest) Sign(peerID string, priv ed25519.PrivateKey) error {
	if m.PeerID == "" {
		m.PeerID = peerID
	}
	if m.IssuedAt.IsZero() {
		m.IssuedAt = time.Now().UTC()
	}
	m.Signature = nil
	payload, err := m.canonical()
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	m.Signature = ed25519.Sign(priv, payload)
	return nil
}

// Verify checks the manifest signature against the issuer's public key. The
// caller must additionally check that m.PeerID matches the transport peer.
func (m *Manifest) Verify(pub ed25519.PublicKey) error {
	if len(m.Signature) == 0 {
		return fmt.Errorf("unsigned manifest")
	}
	payload, err := m.canonical()
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if !ed25519.Verify(pub, payload, m.Signature) {
		return fmt.Errorf("invalid manifest signature")
	}
	return nil
}

// Fingerprint returns the sha256 of the manifest's canonical signed fields.
// It identifies a manifest revision for cheap comparison.
func (m *Manifest) Fingerprint() string {
	payload, err := m.canonical()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// SaveTo writes the manifest atomically to <dir>/<agent-id>.manifest.json.
func (m *Manifest) SaveTo(dir string) error {
	if !ValidAgentID(m.AgentID) {
		return fmt.Errorf("invalid agent id %q", m.AgentID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, m.AgentID+".manifest.json")
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// LoadAll reads every *.manifest.json under dir, skipping invalid files.
func LoadAll(dir string) []Manifest {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Manifest
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}
