//go:build !economy

package gateway

import (
	"net/http"

	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// newNetworkEconomyHandler returns a 404 in the default build — the
// paired-settlement endpoints are bucket-B and only exist under the
// `economy` build tag (ALIGNMENT.md §3).
func newNetworkEconomyHandler(m *mesh.Mesh, authToken string) http.Handler {
	return http.NotFoundHandler()
}
