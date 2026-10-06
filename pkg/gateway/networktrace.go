package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/swarm"
)

// traceAuditScan bounds how many audit entries the trace endpoint reads.
const traceAuditScan = 2000

// traceAuditKeep caps the number of audit lines returned in one report.
const traceAuditKeep = 100

// traceReport correlates one id across the task store, swarm offer/run
// records, the activity feed, and the audit trail.
type traceReport struct {
	ID     string                 `json:"id"`
	Task   *mesh.MeshTaskSnapshot `json:"task,omitempty"`
	Offer  *swarm.OfferInfo       `json:"offer,omitempty"`
	Run    *swarm.RunRecord       `json:"run,omitempty"`
	Events []mesh.ActivityEntry   `json:"events,omitempty"`
	Audit  []json.RawMessage      `json:"audit,omitempty"`
}

// networkTraceHandler correlates task/offer/run ids across stores.
//
//	GET /network/trace?task=<id> | ?offer=<id> | ?run=<id> | ?id=<id>
//	?id resolves all three lookups and reports every section that matched.
type networkTraceHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkTraceHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkTraceHandler{mesh: m, authToken: authToken}
}

func (h *networkTraceHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mesh disabled"})
		return
	}

	q := r.URL.Query()
	var rep traceReport
	switch {
	case q.Get("task") != "":
		rep.ID = q.Get("task")
		if snap, ok := h.mesh.TaskInfo(rep.ID); ok {
			rep.Task = &snap
		}
	case q.Get("offer") != "":
		rep.ID = q.Get("offer")
		if info, ok := traceOffer(rep.ID); ok {
			rep.Offer = &info
		}
	case q.Get("run") != "":
		rep.ID = q.Get("run")
		if rec, ok := traceRun(rep.ID); ok {
			rep.Run = &rec
		}
	case q.Get("id") != "":
		rep.ID = q.Get("id")
		if snap, ok := h.mesh.TaskInfo(rep.ID); ok {
			rep.Task = &snap
		}
		if info, ok := traceOffer(rep.ID); ok {
			rep.Offer = &info
		}
		if rec, ok := traceRun(rep.ID); ok {
			rep.Run = &rec
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "want task=, offer=, run=, or id="})
		return
	}

	rep.Events = h.mesh.ActivityFiltered(0, mesh.ActivityFilter{Contains: rep.ID})
	rep.Audit = h.auditMatches(rep.ID)

	if rep.Task == nil && rep.Offer == nil && rep.Run == nil &&
		len(rep.Events) == 0 && len(rep.Audit) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no trace for " + rep.ID})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// auditMatches returns audit lines mentioning id (newest last), scanned from
// the tail of the trail.
func (h *networkTraceHandler) auditMatches(id string) []json.RawMessage {
	path := h.mesh.AuditPath()
	if path == "" || id == "" {
		return nil
	}
	raws, err := mesh.ReadAuditTail(path, traceAuditScan)
	if err != nil {
		return nil
	}
	var out []json.RawMessage
	for _, raw := range raws {
		if strings.Contains(string(raw), id) {
			out = append(out, raw)
		}
	}
	if len(out) > traceAuditKeep {
		out = out[len(out)-traceAuditKeep:]
	}
	return out
}

// traceOffer resolves an offer id against the daemon's swarm layer.
func traceOffer(id string) (swarm.OfferInfo, bool) {
	sw := currentSwarm()
	if sw == nil {
		return swarm.OfferInfo{}, false
	}
	return sw.OfferInfo(id)
}

// traceRun resolves a run id against the daemon's swarm layer.
func traceRun(id string) (swarm.RunRecord, bool) {
	sw := currentSwarm()
	if sw == nil {
		return swarm.RunRecord{}, false
	}
	return sw.RunRecord(id)
}

func (h *networkTraceHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}
