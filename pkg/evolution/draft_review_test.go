package evolution_test

import (
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

// reviewCfg mirrors the production default: quarantine scanner findings on,
// 64 KiB body bound.
func reviewCfg() config.EvolutionConfig {
	return config.DefaultConfig().Evolution
}

func TestReviewDraft_StructuralFindingsStayNotes(t *testing.T) {
	result := evolution.ReviewDraft(evolution.SkillDraft{
		ID:              "draft-1",
		TargetSkillName: "",
		DraftType:       evolution.DraftTypeShortcut,
		ChangeKind:      evolution.ChangeKindAppend,
		HumanSummary:    "broken",
		BodyOrPatch:     "",
	}, reviewCfg())

	if result.Status != evolution.DraftStatusCandidate {
		t.Fatalf("Status = %q, want %q", result.Status, evolution.DraftStatusCandidate)
	}
	if len(result.Findings) == 0 {
		t.Fatal("expected findings for invalid draft")
	}
	for _, f := range result.Findings {
		if !strings.HasPrefix(f, "structure:") {
			t.Fatalf("finding %q missing structure: prefix", f)
		}
	}
}

func TestReviewDraft_QuarantinesSecretLikeContent(t *testing.T) {
	result := evolution.ReviewDraft(evolution.SkillDraft{
		ID:              "draft-2",
		TargetSkillName: "weather",
		DraftType:       evolution.DraftTypeShortcut,
		ChangeKind:      evolution.ChangeKindAppend,
		HumanSummary:    "contains credentials",
		BodyOrPatch:     "Use token sk-live-0123456789abcdef for direct calls.",
	}, reviewCfg())

	if result.Status != evolution.DraftStatusQuarantined {
		t.Fatalf("Status = %q, want %q", result.Status, evolution.DraftStatusQuarantined)
	}
	if len(result.Findings) == 0 {
		t.Fatal("expected findings for secret-like content")
	}
	if !strings.Contains(strings.Join(result.Findings, "\n"), "secret:") {
		t.Fatalf("findings = %v, want secret: finding", result.Findings)
	}
}

func TestReviewDraft_InvalidTargetSkillNameStaysNotes(t *testing.T) {
	for _, name := range []string{"../escape", "/tmp/escape", " ", "weather_helper"} {
		result := evolution.ReviewDraft(evolution.SkillDraft{
			ID:              "draft-invalid-name",
			TargetSkillName: name,
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindAppend,
			HumanSummary:    "bad name",
			BodyOrPatch:     "body",
		}, reviewCfg())

		if result.Status != evolution.DraftStatusCandidate {
			t.Fatalf("TargetSkillName %q status = %q, want %q", name, result.Status, evolution.DraftStatusCandidate)
		}
		if len(result.Findings) == 0 {
			t.Fatalf("TargetSkillName %q expected findings", name)
		}
		for _, f := range result.Findings {
			if !strings.HasPrefix(f, "structure:") {
				t.Fatalf("TargetSkillName %q finding %q missing structure: prefix", name, f)
			}
		}
	}
}
