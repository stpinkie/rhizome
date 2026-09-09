package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/blob"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
	"github.com/stpinkie/rhizome/pkg/skills"
)

// SkillProtocolID is the libp2p protocol for sharing skills between trusted
// peers: list what a peer offers (mesh.skill_share) and pull a packed skill
// bundle (transferred as a blob).
const SkillProtocolID = protocol.ID("/rhizome/skill/1.0.0")

const (
	skillFrameRequest  = byte(1)
	skillFrameResponse = byte(2)
)

// skillRequest is the signed control message for skill list/pull.
type skillRequest struct {
	Op   string `json:"op"` // "list" | "pull"
	Name string `json:"name,omitempty"`

	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// skillResponse answers list/pull. Pull returns a blob:// ref the caller
// fetches over /rhizome/blob/1.0.0.
type skillResponse struct {
	OK     bool     `json:"ok"`
	Error  string   `json:"error,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Ref    string   `json:"ref,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// shareableSkills returns installed skills that are in the skill_share
// allowlist. Empty allowlist means nothing is shared (deny-all default).
func (m *Mesh) shareableSkills() []string {
	if len(m.cfg.SkillShare) == 0 {
		return nil
	}
	allow := make(map[string]bool, len(m.cfg.SkillShare))
	for _, s := range m.cfg.SkillShare {
		allow[strings.TrimSpace(s)] = true
	}
	m.skillsLoaderMu.RLock()
	loader := m.skillsLoader
	m.skillsLoaderMu.RUnlock()
	if loader == nil {
		return nil
	}
	var out []string
	for _, info := range loader.ListSkills() {
		if allow[info.Name] {
			out = append(out, info.Name)
		}
	}
	sort.Strings(out)
	return out
}

// ShareableSkills exposes the local skill_share ∩ installed list for status
// and API output.
func (m *Mesh) ShareableSkills() []string { return m.shareableSkills() }

// PeerIDString returns this node's peer id string.
func (m *Mesh) PeerIDString() string { return m.host.ID().String() }

// startSkill registers the skill protocol handler. Called from Start; the
// protocol is only served while the mesh is running.
func (m *Mesh) startSkill() {
	m.host.SetStreamHandler(SkillProtocolID, m.handleSkillStream)
}

// skillCall sends a signed skill request and returns the signed response.
func (m *Mesh) skillCall(ctx context.Context, pid peer.ID, req skillRequest) (skillResponse, error) {
	if !m.isTrusted(pid) {
		return skillResponse{}, fmt.Errorf("peer %s is not trusted", pid)
	}
	req.Nonce = newTaskNonce()
	req.Timestamp = time.Now().Unix()
	req.Signature = nil
	payload, err := json.Marshal(req)
	if err != nil {
		return skillResponse{}, fmt.Errorf("encode skill request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)

	s, err := p2putil.OpenProtocolStream(ctx, m.host, pid, SkillProtocolID, 15*time.Second)
	if err != nil {
		return skillResponse{}, fmt.Errorf("peer %s does not support %s: %w", pid, SkillProtocolID, err)
	}
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer rc.Close()

	if err = rc.WriteFrame(skillFrameRequest, payloadWithSig(req)); err != nil {
		return skillResponse{}, fmt.Errorf("write skill request: %w", err)
	}
	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != skillFrameResponse {
		return skillResponse{}, fmt.Errorf("read skill response: %w", err)
	}
	var resp skillResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return skillResponse{}, fmt.Errorf("decode skill response: %w", err)
	}
	// Verify the response was signed by the stream peer.
	sig := resp.Signature
	resp.Signature = nil
	payload, err = json.Marshal(resp)
	if err != nil {
		return skillResponse{}, fmt.Errorf("encode skill response: %w", err)
	}
	pub := m.host.Peerstore().PubKey(pid)
	if pub == nil {
		return skillResponse{}, fmt.Errorf("no public key for peer %s", pid)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil || !ok {
		return skillResponse{}, fmt.Errorf("invalid skill response signature")
	}
	return resp, nil
}

// payloadWithSig re-marshals a signed request for the wire.
func payloadWithSig(req skillRequest) []byte {
	data, _ := json.Marshal(req)
	return data
}

// handleSkillStream services one inbound skill request.
func (m *Mesh) handleSkillStream(s network.Stream) {
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer rc.Close()

	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != skillFrameRequest {
		return
	}
	var req skillRequest
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	resp := m.handleSkillRequest(s.Conn().RemotePeer(), req)

	// Sign the response.
	resp.Signature = nil
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	resp.Signature = identity.Sign(m.id.PrivateKey, payload)
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(skillFrameResponse, data)
}

// handleSkillRequest authorizes and serves a skill list/pull request.
func (m *Mesh) handleSkillRequest(from peer.ID, req skillRequest) skillResponse {
	started := time.Now()
	reject := func(msg string) skillResponse {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "skill." + req.Op,
			"error":   msg,
			"peer_id": from.String(),
		})
		m.auditMesh(from, "skill."+req.Op, "", req.Name, "rejected", started, msg)
		return skillResponse{OK: false, Error: msg}
	}

	if !m.isTrusted(from) {
		return reject("peer is not trusted")
	}
	if err := m.replay.check(from, req.Nonce, req.Timestamp); err != nil {
		return reject(fmt.Sprintf("replay check failed: %v", err))
	}
	if !m.allowRate(from) {
		return reject("rate_limited: skill request rate exceeded")
	}

	// Verify the request signature binds op+name+nonce to the stream peer.
	sig := req.Signature
	req.Signature = nil
	payload, err := json.Marshal(req)
	if err != nil {
		return reject("encode request")
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return reject("no public key for peer")
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil || !ok {
		return reject("invalid signature")
	}

	switch req.Op {
	case "list":
		m.auditMesh(from, "skill.list", "", "", "ok", started, "")
		return skillResponse{OK: true, Skills: m.shareableSkills()}
	case "pull":
		return m.serveSkillPull(from, req.Name, started)
	default:
		return reject(fmt.Sprintf("unknown op %q", req.Op))
	}
}

// serveSkillPull packs the requested skill and returns a blob:// ref.
func (m *Mesh) serveSkillPull(from peer.ID, name string, started time.Time) skillResponse {
	reject := func(msg string) skillResponse {
		m.auditMesh(from, "skill.pull", "", name, "rejected", started, msg)
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "skill.pull",
			"error":   msg,
			"peer_id": from.String(),
		})
		return skillResponse{OK: false, Error: msg}
	}

	if err := skills.ValidateSkillName(name); err != nil {
		return reject("invalid skill name")
	}
	allowed := false
	for _, s := range m.shareableSkills() {
		if s == name {
			allowed = true
			break
		}
	}
	if !allowed {
		return reject("skill is not shared by this peer")
	}
	if !m.BlobEnabled() {
		return reject("blob transfer is not enabled on this peer")
	}

	m.skillsLoaderMu.RLock()
	loader := m.skillsLoader
	m.skillsLoaderMu.RUnlock()
	dir, found := loader.SkillDir(name)
	if !found {
		return reject("skill not installed")
	}

	tmp, err := os.CreateTemp("", "rhizome-skill-*.zip")
	if err != nil {
		return reject("pack skill")
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := skills.PackSkillDir(dir, tmpPath); err != nil {
		return reject(fmt.Sprintf("pack skill: %v", err))
	}
	hash, err := m.blobStore.PutFile(tmpPath, blob.Meta{
		Name:        name + ".zip",
		ContentType: "application/zip",
	})
	if err != nil {
		return reject(fmt.Sprintf("store bundle: %v", err))
	}

	m.auditMesh(from, "skill.pull", "", name+"/"+hash[:12], "ok", started, "")
	m.publishMeshEvent(runtimeevents.KindMeshSkillPush, map[string]any{
		"peer_id": from.String(),
		"skill":   name,
		"hash":    hash,
	})
	return skillResponse{OK: true, Ref: blob.MakeRef(m.host.ID().String(), hash)}
}

// ListPeerSkills returns the skills the peer advertises as shareable.
func (m *Mesh) ListPeerSkills(ctx context.Context, pid peer.ID) ([]string, error) {
	resp, err := m.skillCall(ctx, pid, skillRequest{Op: "list"})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("skill list rejected: %s", resp.Error)
	}
	return resp.Skills, nil
}

// PullSkillResult reports a completed skill pull.
type PullSkillResult struct {
	Name       string   `json:"name"`
	Dir        string   `json:"dir"`
	Suspicious bool     `json:"suspicious"`
	Matches    []string `json:"matches,omitempty"`
	Ref        string   `json:"ref"`
}

// PullSkill fetches a skill bundle from a trusted peer and installs it under
// the local global skills directory (~/.rhizome/skills/<name>) with
// mesh:<peer-id> origin metadata. The bundle is guard-scanned; suspicious
// content is flagged but still installed (the guard verdict is advisory).
func (m *Mesh) PullSkill(ctx context.Context, pid peer.ID, name string) (PullSkillResult, error) {
	if err := skills.ValidateSkillName(name); err != nil {
		return PullSkillResult{}, err
	}
	started := time.Now()
	resp, err := m.skillCall(ctx, pid, skillRequest{Op: "pull", Name: name})
	if err != nil {
		m.auditMesh(pid, "skill.pull.req", "", name, "error", started, err.Error())
		return PullSkillResult{}, err
	}
	if !resp.OK || resp.Ref == "" {
		m.auditMesh(pid, "skill.pull.req", "", name, "rejected", started, resp.Error)
		return PullSkillResult{}, fmt.Errorf("skill pull rejected: %s", resp.Error)
	}

	local, _, err := m.FetchBlob(ctx, resp.Ref)
	if err != nil {
		return PullSkillResult{}, fmt.Errorf("fetch skill bundle: %w", err)
	}

	// Install into the global skills root so the skill survives workspace
	// sync and carries a mesh:<peer> origin marker.
	m.skillsLoaderMu.RLock()
	loader := m.skillsLoader
	m.skillsLoaderMu.RUnlock()
	installRoot := ""
	if loader != nil {
		installRoot = loader.GlobalSkillsDir()
	}
	if installRoot == "" {
		return PullSkillResult{}, fmt.Errorf("no global skills directory configured")
	}
	dest := filepath.Join(installRoot, name)
	if err := os.RemoveAll(dest); err != nil {
		return PullSkillResult{}, fmt.Errorf("clear previous install: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return PullSkillResult{}, err
	}

	scan, err := skills.UnpackSkillBundle(local, name, dest)
	if err != nil {
		_ = os.RemoveAll(dest)
		return PullSkillResult{}, err
	}
	if err := skills.WriteSkillOrigin(dest, "mesh", "mesh:"+pid.String()); err != nil {
		return PullSkillResult{}, fmt.Errorf("write origin metadata: %w", err)
	}

	m.auditMesh(pid, "skill.pull", "", name, "ok", started, "")
	m.publishMeshEvent(runtimeevents.KindMeshSkillPull, map[string]any{
		"peer_id":    pid.String(),
		"skill":      name,
		"suspicious": scan.Suspicious,
	})
	return PullSkillResult{
		Name:       name,
		Dir:        dest,
		Suspicious: scan.Suspicious,
		Matches:    scan.Matches,
		Ref:        resp.Ref,
	}, nil
}
