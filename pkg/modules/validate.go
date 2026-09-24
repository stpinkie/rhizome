// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
)

// coreProtocolIDs are the libp2p protocol ids the node itself binds. A
// module's declared protocols are bridged wire access — they must never
// shadow core traffic, so claiming one of these is a config error.
//
// NOTE: "/rhizome/acp/1.0.0" is deliberately absent — it is conditionally
// core-claimed (only while acp.server.remote serves). The bridge resolves
// that claim dynamically against the live handler set at startup so the
// market module can declare it whenever core is not serving.
var coreProtocolIDs = map[string]bool{
	"/rhizome/caps/1.0.0":       true,
	"/rhizome/agent/1.0.0":      true,
	"/rhizome/agent-task/1.0.0": true,
	"/rhizome/blob/1.0.0":       true,
	"/rhizome/skill/1.0.0":      true,
	"/rhizome/pair/1.0.0":       true,
	"/rhizome/swarm/1.0.0":      true,
	"/rhizome/git-sync/1.0.0":   true,
}

// protocolIDPattern matches well-formed libp2p protocol ids: a leading
// slash plus path segments of printable, whitespace-free characters.
var protocolIDPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,127}$`)

// ValidateProtocols checks one module's declared protocol list: each id
// must be well-formed and must not collide with a core-registered
// protocol. Returns the first problem found.
func ValidateProtocols(moduleID string, protos []string) error {
	seen := map[string]bool{}
	for _, p := range protos {
		if !protocolIDPattern.MatchString(p) || strings.Contains(p, "//") {
			return fmt.Errorf("module %q: malformed protocol id %q", moduleID, p)
		}
		if coreProtocolIDs[p] {
			return fmt.Errorf("module %q: protocol %q collides with a core-registered protocol", moduleID, p)
		}
		if seen[p] {
			return fmt.Errorf("module %q: protocol %q declared twice", moduleID, p)
		}
		seen[p] = true
	}
	return nil
}

// ValidateConfig checks cfg.Modules against the catalog. pkg/config cannot
// do this itself (it must not import the catalog — that would create an
// import cycle), so validation lives here and is called by the daemon
// startup path, the CLI, and the web API before applying changes. lookup
// resolves module ids — pass a Manager's LookupSpec so modules from a
// configured remote index validate, or the package-level Lookup for the
// embedded-only set.
//
// Rules:
//   - every configured id must exist in the catalog
//   - every configured field/secret key must exist in the module's fields
//   - non-secret fields go in fields; secret fields go in secrets
//   - enabled modules must support this platform and have all required
//     fields set
func ValidateConfig(cfg *config.Config, lookup func(string) (ModuleSpec, bool)) error {
	if lookup == nil {
		lookup = Lookup
	}
	if len(cfg.Modules) == 0 {
		return nil
	}
	var errs []string
	ids := make([]string, 0, len(cfg.Modules))
	for id := range cfg.Modules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	claimed := map[string]string{} // protocol → first enabled module claiming it

	for _, id := range ids {
		mc := cfg.Modules[id]
		spec, ok := lookup(id)
		if !ok {
			errs = append(errs, fmt.Sprintf("modules.%s: unknown module id", id))
			continue
		}
		if err := ValidateProtocols(id, spec.Protocols); err != nil {
			errs = append(errs, err.Error())
		}
		for k := range mc.Fields {
			f, ok := spec.Field(k)
			if !ok {
				errs = append(errs, fmt.Sprintf("modules.%s.fields.%s: unknown field", id, k))
				continue
			}
			if f.Secret {
				errs = append(errs, fmt.Sprintf("modules.%s.fields.%s: secret field must live under secrets", id, k))
			}
		}
		for k := range mc.Secrets {
			f, ok := spec.Field(k)
			if !ok {
				errs = append(errs, fmt.Sprintf("modules.%s.secrets.%s: unknown field", id, k))
				continue
			}
			if !f.Secret {
				errs = append(errs, fmt.Sprintf(
					"modules.%s.secrets.%s: non-secret field must live under fields", id, k))
			}
		}
		if mc.Enabled {
			if !spec.Supports(Platform()) {
				errs = append(errs, fmt.Sprintf("modules.%s: not supported on %s", id, Platform()))
			}
			values := map[string]string{}
			for _, f := range spec.ConfigFields {
				if f.Default != "" {
					values[f.Key] = f.Default
				}
			}
			for k, v := range mc.Fields {
				values[k] = v
			}
			for k, v := range mc.Secrets {
				values[k] = v.String()
			}
			for _, f := range spec.ConfigFields {
				if f.Required && values[f.Key] == "" {
					errs = append(errs, fmt.Sprintf("modules.%s: required field %q is unset", id, f.Key))
				}
			}
			// Two enabled modules cannot claim the same wire protocol —
			// the bridge can only splice each protocol to one module.
			for _, p := range spec.Protocols {
				if prior, dup := claimed[p]; dup {
					errs = append(errs, fmt.Sprintf(
						"modules.%s: protocol %q also claimed by enabled module %q", id, p, prior))
				} else {
					claimed[p] = id
				}
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid module configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
