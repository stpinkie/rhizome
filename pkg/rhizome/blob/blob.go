// Package blob implements content-addressed file transfer between trusted
// Rhizome peers over /rhizome/blob/1.0.0. Blobs are stored by their SHA-256
// hash under a per-node directory; peers push blobs (PUT) or pull them
// (GET/STAT) so remote agent tasks can carry attachments and return
// artifacts.
//
// Security model: every control frame is signed with the sender's node key
// and carries a nonce+timestamp for replay protection, matching the agent
// and task protocols. Authorization (trust, ACL, rate limits, audit) is
// supplied by the caller via the Authorize hook so this package stays
// decoupled from the mesh implementation.
package blob

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolID is the libp2p protocol for mesh blob transfer.
const ProtocolID = protocol.ID("/rhizome/blob/1.0.0")

const (
	frameRequest  = byte(1)
	frameResponse = byte(2)
	frameChunk    = byte(3)

	// chunkSize is the payload size of one data frame while streaming blob
	// contents. ReliableConn ACKs each frame, so moderate chunks keep
	// retransmit cost low on lossy links.
	chunkSize = 256 * 1024

	// maxHashLen guards against oversized hash strings in requests.
	maxHashLen = 128
	// maxNameLen bounds blob metadata strings received from peers.
	maxNameLen = 512
)

// Op identifies a blob-protocol operation.
type Op string

const (
	// OpStat returns a blob's metadata without transferring content.
	OpStat Op = "stat"
	// OpGet streams a blob's content to the requester.
	OpGet Op = "get"
	// OpPut announces an incoming blob; content follows as chunk frames.
	OpPut Op = "put"
	// OpCommit finishes a put after all chunks are sent; the server verifies
	// the received content hash before storing.
	OpCommit Op = "commit"
)

// Meta describes a stored blob.
type Meta struct {
	// Name is the original filename hint (sanitized on receipt).
	Name string `json:"name,omitempty"`
	// ContentType is the MIME type hint supplied by the uploader.
	ContentType string `json:"content_type,omitempty"`
	// Owner is the peer id that stored the blob on this node.
	Owner string `json:"owner,omitempty"`
	// StoredAt is the unix time the blob was stored locally.
	StoredAt int64 `json:"stored_at,omitempty"`
	// Size is the blob size in bytes.
	Size int64 `json:"size,omitempty"`
}

// Request is the signed control message for every blob operation. The
// signature covers the canonical JSON encoding of all fields except
// Signature itself, binding op + hash + freshness to the sender's identity.
type Request struct {
	Op          Op     `json:"op"`
	Hash        string `json:"hash,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`

	// Nonce and Timestamp are anti-replay fields, verified by the callee.
	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// Response answers every blob request, including rejections. Data-carrying
// ops (get, put/commit) use it as both the handshake and the final status.
type Response struct {
	OK          bool   `json:"ok"`
	Hash        string `json:"hash,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Name        string `json:"name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Error       string `json:"error,omitempty"`
	Signature   []byte `json:"signature,omitempty"`
}

// refPrefix prefixes a fully qualified blob reference:
// blob://<peer-id>/<sha256-hex>.
const refPrefix = "blob://"

// MakeRef returns a fully qualified blob reference for a hash hosted by pid.
func MakeRef(peerID, hash string) string {
	return refPrefix + peerID + "/" + hash
}

// ParseRef splits a blob reference into its peer id and hash. A bare
// "blob://<hash>" or "<hash>" without a peer segment returns an empty peer
// id so callers can treat it as a local reference.
func ParseRef(ref string) (peerID, hash string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty blob ref")
	}
	if strings.HasPrefix(ref, refPrefix) {
		rest := strings.TrimPrefix(ref, refPrefix)
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			peerID = rest[:i]
			hash = rest[i+1:]
		} else {
			hash = rest
		}
	} else {
		hash = ref
	}
	if err := ValidateHash(hash); err != nil {
		return "", "", err
	}
	return peerID, hash, nil
}

// ValidateHash checks that s is a lowercase hex SHA-256 digest.
func ValidateHash(s string) error {
	if len(s) != 64 {
		return fmt.Errorf("blob hash must be 64 hex chars, got %d", len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		return fmt.Errorf("blob hash is not hex: %w", err)
	}
	if s != strings.ToLower(s) {
		return fmt.Errorf("blob hash must be lowercase hex")
	}
	return nil
}

// validateRequest performs shape checks common to every op.
func (r *Request) validate() error {
	switch r.Op {
	case OpStat, OpGet, OpCommit:
		if err := ValidateHash(r.Hash); err != nil {
			return err
		}
	case OpPut:
		if err := ValidateHash(r.Hash); err != nil {
			return err
		}
		if r.Size <= 0 {
			return fmt.Errorf("blob size must be positive")
		}
	default:
		return fmt.Errorf("unknown blob op %q", r.Op)
	}
	if len(r.Hash) > maxHashLen || len(r.Name) > maxNameLen || len(r.ContentType) > maxNameLen {
		return fmt.Errorf("blob request field too large")
	}
	return nil
}
