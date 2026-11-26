//go:build !economy

package api

import "net/http"

// Bucket-B quarantine stubs (ALIGNMENT.md §3): the market dashboard and
// paired-settlement endpoints only exist under the `economy` build tag.
// Route registration stays so callers compile; the handlers simply 404.

func (h *Handler) registerMarketRoutes(mux *http.ServeMux) {}

func (h *Handler) registerNetworkEconomyRoutes(mux *http.ServeMux) {}
