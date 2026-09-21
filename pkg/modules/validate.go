// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
)

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

	for _, id := range ids {
		mc := cfg.Modules[id]
		spec, ok := lookup(id)
		if !ok {
			errs = append(errs, fmt.Sprintf("modules.%s: unknown module id", id))
			continue
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
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid module configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
