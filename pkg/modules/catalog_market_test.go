//go:build economy

package modules

import (
	"strings"
	"testing"
)

// TestRhizomeMarketEntry pins the first-party module's catalog contract:
// daemon kind, the ACP protocol claim, the releases:[] chicken-and-egg
// posture, and asset-name resolution per platform.
func TestRhizomeMarketEntry(t *testing.T) {
	spec, ok := Lookup("rhizome-market")
	if !ok {
		t.Fatal("rhizome-market missing from catalog")
	}
	if spec.Kind != KindDaemon {
		t.Fatalf("kind = %q", spec.Kind)
	}
	if err := ValidateProtocols(spec.ID, spec.Protocols); err != nil {
		t.Fatalf("protocols invalid: %v", err)
	}
	if len(spec.Protocols) != 1 || spec.Protocols[0] != "/rhizome/acp/1.0.0" {
		t.Fatalf("protocols = %v", spec.Protocols)
	}
	// releases:[] → present but uninstallable until the first pin.
	if _, ok := spec.Release(""); ok {
		t.Fatal("empty releases must not resolve")
	}
	// Asset templates only use placeholders Asset() can expand —
	// {goos}/{goarch}/{version}/{build}; a stray placeholder (e.g. {ext})
	// would ship a literal in the resolved filename.
	templates := make([]string, 0, 1+len(spec.Install.AssetTemplates))
	templates = append(templates, spec.Install.AssetTemplate)
	for _, over := range spec.Install.AssetTemplates {
		templates = append(templates, over)
	}
	for _, tmpl := range templates {
		for _, ph := range []string{"{goos}", "{goarch}", "{version}", "{build}"} {
			tmpl = strings.ReplaceAll(tmpl, ph, "")
		}
		if strings.Contains(tmpl, "{") {
			t.Fatalf("unresolvable placeholder in asset template %q", tmpl)
		}
	}
}
