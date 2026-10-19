package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// EvolutionOriginFile is the metadata marker written into a skill directory
// by the evolution apply path — every committed skill-draft apply writes it
// so evolved skills are marked, never silently laundered (the
// .skill-origin.json mesh-install precedent, schema-parallel so both
// markers parse with one reader).
const EvolutionOriginFile = ".evolution-origin.json"

// EvolutionOriginKind is the origin_kind value marking machine-evolved
// skills (vs the mesh install marker's "mesh").
const EvolutionOriginKind = "evolution"

// EvolutionOriginMeta records which evolution draft produced the skill's
// current content. AppliedAt is Unix milliseconds (mirroring
// SkillOriginMeta.InstalledAt); Review is "auto" when the cold path applied
// the draft (apply_require_review=false) or "accept" when an operator
// accepted it manually.
type EvolutionOriginMeta struct {
	Version         int    `json:"version"`
	OriginKind      string `json:"origin_kind"`
	DraftID         string `json:"draft_id"`
	SourcePatternID string `json:"source_pattern_id"`
	AppliedAt       int64  `json:"applied_at"`
	Review          string `json:"review"`
}

// WriteEvolutionOrigin records evolution provenance for an applied skill
// directory — atomically, owner-only like WriteSkillOrigin.
func WriteEvolutionOrigin(dir string, meta EvolutionOriginMeta) error {
	if meta.Version == 0 {
		meta.Version = 1
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, EvolutionOriginFile), data, 0o600)
}

// ReadEvolutionOrigin loads the marker written by WriteEvolutionOrigin —
// absent or malformed markers surface as errors, not fabricated metadata.
func ReadEvolutionOrigin(dir string) (EvolutionOriginMeta, error) {
	var meta EvolutionOriginMeta
	data, err := os.ReadFile(filepath.Join(dir, EvolutionOriginFile))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("parse %s: %w", EvolutionOriginFile, err)
	}
	return meta, nil
}
