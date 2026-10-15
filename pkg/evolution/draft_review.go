package evolution

import (
	"fmt"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/guard"
	"github.com/stpinkie/rhizome/pkg/redact"
)

type DraftReviewResult struct {
	Status      DraftStatus
	Findings    []string
	ReviewNotes []string
}

// ReviewDraft runs the hardened review pipeline over a generated draft.
// Findings land prefixed so `draft show` explains the decision:
//
//	structure:<check> — local structural validation failures (ValidateDraft).
//	  These stay notes — a malformed draft is a quality signal, not a
//	  security one; apply-time validateAppliedSkillBody is the hard gate.
//	guard:injection:<path>: <match> — prompt-injection phrases from
//	  pkg/guard, over body_or_patch + human_summary directly and the
//	  structured fields through the recursive arg-scan idiom.
//	secret:<kind> in <field> — credential-shaped substrings from
//	  pkg/redact's shared secret-pattern set.
//	size:… — body_or_patch over max_draft_bytes; a resource bound, so it
//	  quarantines unconditionally.
//
// guard:/secret:/size: findings quarantine the draft; guard:/secret: demote
// to findings-only when evolution.review_auto_quarantine is false. A
// quarantined draft moves only via `draft accept --force` (explicit,
// logged) or `draft reject` — never silently forward.
func ReviewDraft(draft SkillDraft, cfg config.EvolutionConfig) DraftReviewResult {
	findings := make([]string, 0, 4)
	quarantine := false

	for _, f := range ValidateDraft(draft) {
		findings = append(findings, "structure:"+f)
	}

	if maxBytes := cfg.EffectiveMaxDraftBytes(); len(draft.BodyOrPatch) > maxBytes {
		findings = append(findings, fmt.Sprintf(
			"size:body_or_patch exceeds max_draft_bytes (%d > %d)",
			len(draft.BodyOrPatch), maxBytes))
		quarantine = true
	}

	scanFindings := scanDraftSecurity(draft)
	findings = append(findings, scanFindings...)
	if len(scanFindings) > 0 && cfg.ReviewAutoQuarantine {
		quarantine = true
	}

	result := DraftReviewResult{
		Status:   DraftStatusCandidate,
		Findings: findings,
		ReviewNotes: []string{
			"local structural validation completed",
			"guard + secret scan completed",
		},
	}
	if quarantine {
		result.Status = DraftStatusQuarantined
	} else if len(scanFindings) > 0 {
		result.ReviewNotes = append(result.ReviewNotes,
			"scan findings recorded; review_auto_quarantine=false left the draft a candidate for manual review")
	}
	return result
}

// scanDraftSecurity returns the guard:/secret: findings for a draft — the
// content checks beyond structural validation.
func scanDraftSecurity(draft SkillDraft) []string {
	findings := make([]string, 0, 4)

	for _, f := range []struct{ name, val string }{
		{"body_or_patch", draft.BodyOrPatch},
		{"human_summary", draft.HumanSummary},
	} {
		if match, ok := guard.ContainsPromptInjection(f.val); ok {
			findings = append(findings, fmt.Sprintf("guard:injection:%s: %q", f.name, match))
		}
		for _, kind := range redact.DetectSecrets(f.val) {
			findings = append(findings, fmt.Sprintf("secret:%s in %s", kind, f.name))
		}
	}

	// Structured fields go through the recursive arg-scan idiom one field at
	// a time so each hit reports its own dotted path.
	for _, f := range []struct {
		name string
		list []string
	}{
		{"intended_use_cases", draft.IntendedUseCases},
		{"preferred_entry_path", draft.PreferredEntryPath},
		{"avoid_patterns", draft.AvoidPatterns},
	} {
		items := make([]any, len(f.list))
		for i, s := range f.list {
			items[i] = s
		}
		if path, match, ok := guard.ScanArgsForInjection(map[string]any{f.name: items}); ok {
			findings = append(findings, fmt.Sprintf("guard:injection:%s: %q", path, match))
		}
		for i, s := range f.list {
			for _, kind := range redact.DetectSecrets(s) {
				findings = append(findings, fmt.Sprintf("secret:%s in %s[%d]", kind, f.name, i))
			}
		}
	}
	return findings
}
