package swarm

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// doctorMemberTimeout bounds each member's MsgQuery round trip.
const doctorMemberTimeout = 5 * time.Second

// DoctorMember is the doctor's view of one roster member: whether it
// answered a MsgQuery, whether it lists us back, and its reported
// shared-state epoch for the swarm (0 when it never coordinated or runs an
// older build without the additive epochs field).
type DoctorMember struct {
	PeerID    string `json:"peer_id"`
	Reachable bool   `json:"reachable"`
	ListsUs   bool   `json:"lists_us"`
	Epoch     int64  `json:"epoch,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DoctorReport answers "is my swarm converged?" — coordinator reachability
// plus the roster asymmetry between members we know and members that list
// us back.
type DoctorReport struct {
	SwarmID       string `json:"swarm_id"`
	SelfID        string `json:"self_id"`
	Coordinator   string `json:"coordinator,omitempty"`
	CoordinatorUp bool   `json:"coordinator_up"`
	Queried       int    `json:"queried"`
	Reachable     int    `json:"reachable"`
	// Asymmetric lists members that answered but do not have us in their
	// roster — they see the swarm without us.
	Asymmetric []string `json:"asymmetric,omitempty"`
	// Undiscovered lists peer ids remote members know for this swarm that
	// are absent from the local roster — we see the swarm without them.
	Undiscovered []string       `json:"undiscovered,omitempty"`
	Members      []DoctorMember `json:"members"`
}

// Doctor queries every known roster member (bounded by max_members, 5s per
// member) with MsgQuery and diffs the returned rosters against the local
// one: members we know vs members who list us. It is a read-only diagnostic
// — no roster or election state changes.
func (s *Swarm) Doctor(ctx context.Context, swarmID string) (DoctorReport, error) {
	if !ValidSwarmID(swarmID) {
		return DoctorReport{}, fmt.Errorf("invalid swarm id %q", swarmID)
	}
	self := s.host.ID().String()
	members := s.Members(swarmID)
	if limit := s.cfg.MaxMembers; limit > 0 && len(members) > limit {
		members = members[:limit]
	}

	known := make(map[string]bool, len(members)+1)
	known[self] = true
	for _, m := range members {
		known[m.PeerID] = true
	}

	report := DoctorReport{
		SwarmID:     swarmID,
		SelfID:      self,
		Coordinator: s.Coordinator(swarmID),
	}
	if report.Coordinator == self {
		report.CoordinatorUp = true
	}
	undiscovered := map[string]bool{}

	for _, m := range members {
		dm := DoctorMember{PeerID: m.PeerID}
		report.Queried++
		pid, err := peer.Decode(m.PeerID)
		if err != nil {
			dm.Error = "invalid peer id in roster"
			report.Members = append(report.Members, dm)
			continue
		}
		qctx, cancel := context.WithTimeout(ctx, doctorMemberTimeout)
		resp, err := s.Request(qctx, pid, Envelope{SwarmID: swarmID, Type: MsgQuery})
		cancel()
		if err != nil {
			dm.Error = err.Error()
			report.Members = append(report.Members, dm)
			continue
		}
		if err := s.verify(pid, resp); err != nil {
			dm.Error = "unverifiable response: " + err.Error()
			report.Members = append(report.Members, dm)
			continue
		}
		if resp.Type != MsgQueryResp {
			dm.Error = "unexpected response type " + string(resp.Type)
			report.Members = append(report.Members, dm)
			continue
		}
		var qr queryRespPayload
		if err := decodePayload(resp, &qr); err != nil {
			dm.Error = "undecodable response: " + err.Error()
			report.Members = append(report.Members, dm)
			continue
		}
		dm.Reachable = true
		report.Reachable++
		dm.Epoch = qr.Epochs[swarmID]
		for _, id := range qr.Members[swarmID] {
			if id == self {
				dm.ListsUs = true
				continue
			}
			if !known[id] {
				undiscovered[id] = true
			}
		}
		if dm.Reachable && !dm.ListsUs {
			report.Asymmetric = append(report.Asymmetric, dm.PeerID)
		}
		if m.PeerID == report.Coordinator {
			report.CoordinatorUp = true
		}
		report.Members = append(report.Members, dm)
	}

	sort.Strings(report.Asymmetric)
	for id := range undiscovered {
		report.Undiscovered = append(report.Undiscovered, id)
	}
	sort.Strings(report.Undiscovered)
	return report, nil
}
