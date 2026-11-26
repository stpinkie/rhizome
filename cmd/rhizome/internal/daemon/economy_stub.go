//go:build !economy

package daemon

import (
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// wireEconSettler is a no-op in the default build — the web3 settle
// backend is bucket-B and only exists under the `economy` build tag.
func wireEconSettler(cfg *config.Config, home string, rhizomeMesh *mesh.Mesh) {
}

// econAdvertHook returns nil in the default build — pair bundles never
// echo economy terms without the `economy` tag.
func econAdvertHook(cfg *config.Config) func() *config.EconAdvert {
	return nil
}
