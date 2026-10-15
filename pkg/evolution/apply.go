package evolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/stpinkie/rhizome/pkg/fileutil"
	"github.com/stpinkie/rhizome/pkg/skills"
)

type Applier struct {
	paths Paths
	now   func() time.Time
}

func NewApplier(paths Paths, now func() time.Time) *Applier {
	if now == nil {
		now = time.Now
	}
	return &Applier{
		paths: paths,
		now:   now,
	}
}

func (a *Applier) ApplyDraft(ctx context.Context, workspace string, draft SkillDraft) error {
	rollback, err := a.applyDraftWithRollback(ctx, workspace, draft)
	if err != nil {
		return err
	}
	_ = rollback
	return nil
}

func (a *Applier) applyDraftWithRollback(
	ctx context.Context,
	workspace string,
	draft SkillDraft,
) (func() error, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if validateErr := skills.ValidateSkillName(draft.TargetSkillName); validateErr != nil {
		return nil, validateErr
	}

	existingBody, backupPath, hadOriginal, err := a.backupCurrentSkill(workspace, draft.TargetSkillName)
	if err != nil {
		return nil, err
	}

	renderedBody, err := renderAppliedBody(draft, existingBody, hadOriginal)
	if err != nil {
		return nil, err
	}

	if err := validateAppliedSkillBody(
		renderedBody,
		draft.TargetSkillName,
		allowsExistingFrontmatterFields(draft.ChangeKind, hadOriginal),
	); err != nil {
		return nil, err
	}

	skillDir := filepath.Join(workspace, "skills", draft.TargetSkillName)
	if mkdirErr := os.MkdirAll(skillDir, 0o755); mkdirErr != nil {
		return nil, mkdirErr
	}

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := fileutil.WriteFileAtomic(skillPath, []byte(renderedBody), 0o644); err != nil {
		return nil, err
	}

	return func() error {
		return a.rollbackSkill(skillPath, backupPath, hadOriginal)
	}, nil
}

func (a *Applier) backupCurrentSkill(
	workspace, skillName string,
) (currentBody, backupPath string, hadOriginal bool, err error) {
	if validateErr := skills.ValidateSkillName(skillName); validateErr != nil {
		return "", "", false, validateErr
	}

	skillPath := filepath.Join(workspace, "skills", skillName, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if os.IsNotExist(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}

	backupDir := filepath.Join(
		a.skillBackupDir(workspace, skillName),
		a.now().Format(backupTimestampLayout),
	)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", "", false, err
	}

	backupPath = filepath.Join(backupDir, "SKILL.md")
	if err := fileutil.WriteFileAtomic(backupPath, data, 0o644); err != nil {
		return "", "", false, err
	}
	return string(data), backupPath, true, nil
}

func (a *Applier) rollbackSkill(skillPath, backupPath string, hadOriginal bool) error {
	if hadOriginal {
		data, err := os.ReadFile(backupPath)
		if err != nil {
			return err
		}
		return fileutil.WriteFileAtomic(skillPath, data, 0o644)
	}
	if err := os.Remove(skillPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	skillDir := filepath.Dir(skillPath)
	if err := os.Remove(skillDir); err != nil && !os.IsNotExist(err) && !isDirNotEmptyError(err) {
		return err
	}
	return nil
}

// backupTimestampLayout is the directory-name format backupCurrentSkill uses
// for each pre-mutation snapshot. The nanosecond suffix keeps same-second
// applies distinct; dirs whose names fail this layout (e.g. "versions") are
// not timestamped backups.
const backupTimestampLayout = "20060102-150405.000000000"

// versionsSubdir names the per-skill subdirectory holding named-version
// snapshots: a post-apply copy of SKILL.md keyed by the version label (the
// draft id). `--to <version>` rollback restores these; timestamped sibling
// dirs hold pre-mutation snapshots used by bare rollback.
const versionsSubdir = "versions"

func (a *Applier) skillBackupDir(workspace, skillName string) string {
	return filepath.Join(a.paths.BackupsDir, workspaceScopeDir(workspace), skillName)
}

// snapshotCurrentSkill writes a timestamped pre-mutation backup of the
// skill's current SKILL.md and returns its path — "" when the skill has no
// current file (pre-create state; nothing to preserve).
func (a *Applier) snapshotCurrentSkill(workspace, skillName string) (string, error) {
	_, backupPath, hadOriginal, err := a.backupCurrentSkill(workspace, skillName)
	if err != nil || !hadOriginal {
		return "", err
	}
	return backupPath, nil
}

// WriteVersionSnapshot records the post-apply content of the skill under
// backups/<ws>/<skill>/versions/<version>/ so `rollback --to <version>` can
// restore it later. Called after the apply's store writes succeed — a failed
// apply rolls the file back and never reaches this.
func (a *Applier) WriteVersionSnapshot(workspace, skillName, version string) error {
	if err := skills.ValidateSkillName(skillName); err != nil {
		return err
	}
	version = sanitizeWorkspaceComponent(strings.TrimSpace(version))
	if version == "" {
		return fmt.Errorf("version label required for snapshot")
	}
	skillPath := filepath.Join(workspace, "skills", skillName, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}
	dir := filepath.Join(a.skillBackupDir(workspace, skillName), versionsSubdir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, "SKILL.md"), data, 0o644)
}

// timestampedBackups lists the skill's pre-mutation snapshot directories
// (oldest first). The versions/ subdirectory and anything else not matching
// the timestamp layout is skipped.
func (a *Applier) timestampedBackups(workspace, skillName string) ([]string, error) {
	dir := a.skillBackupDir(workspace, skillName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, perr := time.Parse(backupTimestampLayout, entry.Name()); perr != nil {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	// Names are timestamp-formatted so lexical order is chronological.
	sort.Strings(out)
	return out, nil
}

// PruneBackups bounds the skill's backup footprint to the newest keep
// timestamped snapshots plus the newest keep version snapshots (by snapshot
// write time). keep <= 0 keeps everything.
func (a *Applier) PruneBackups(workspace, skillName string, keep int) error {
	if keep <= 0 {
		return nil
	}
	timestamped, err := a.timestampedBackups(workspace, skillName)
	if err != nil {
		return err
	}
	for _, dir := range timestamped[:max(len(timestamped)-keep, 0)] {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}

	versionsDir := filepath.Join(a.skillBackupDir(workspace, skillName), versionsSubdir)
	entries, err := os.ReadDir(versionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type versionDir struct {
		path string
		at   time.Time
	}
	versions := make([]versionDir, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, ierr := entry.Info()
		if ierr != nil {
			return ierr
		}
		versions = append(versions, versionDir{
			path: filepath.Join(versionsDir, entry.Name()),
			at:   info.ModTime(),
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].at.Before(versions[j].at) })
	for _, v := range versions[:max(len(versions)-keep, 0)] {
		if err := os.RemoveAll(v.path); err != nil {
			return err
		}
	}
	return nil
}

func isDirNotEmptyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "directory not empty")
}

func validateAppliedSkillBody(body, targetSkillName string, allowExtraFrontmatterFields bool) error {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "---\n") {
		return fmt.Errorf("skill frontmatter is required")
	}
	if !strings.Contains(body, "\n# ") {
		return fmt.Errorf("skill heading is required")
	}
	frontmatter, _ := splitSkillFrontmatter(body)
	fields, err := parseSkillFrontmatterFields(frontmatter, allowExtraFrontmatterFields)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(fields["name"])
	if name == "" {
		return fmt.Errorf("skill frontmatter name is required")
	}
	if name != targetSkillName {
		return fmt.Errorf("skill frontmatter name %q does not match target skill %q", name, targetSkillName)
	}
	if strings.TrimSpace(fields["description"]) == "" {
		return fmt.Errorf("skill frontmatter description is required")
	}
	return nil
}

func allowsExistingFrontmatterFields(kind ChangeKind, hadOriginal bool) bool {
	return hadOriginal && (kind == ChangeKindAppend || kind == ChangeKindMerge)
}

func renderAppliedBody(draft SkillDraft, existingBody string, hadOriginal bool) (string, error) {
	switch draft.ChangeKind {
	case ChangeKindCreate:
		if hadOriginal {
			return "", fmt.Errorf("cannot create skill %q: skill already exists", draft.TargetSkillName)
		}
		return renderDeployableSkillBody(draft.BodyOrPatch), nil
	case ChangeKindReplace:
		if !hadOriginal {
			return "", fmt.Errorf("cannot replace skill %q: skill does not exist", draft.TargetSkillName)
		}
		return renderDeployableSkillBody(draft.BodyOrPatch), nil
	case ChangeKindAppend:
		patch, err := renderDeployablePatchBody(draft.BodyOrPatch, draft.TargetSkillName)
		if err != nil {
			return "", err
		}
		if !hadOriginal || strings.TrimSpace(existingBody) == "" {
			return renderDeployableSkillBody(draft.BodyOrPatch), nil
		}
		return strings.TrimRight(existingBody, "\n") + "\n\n" + strings.TrimLeft(patch, "\n"), nil
	case ChangeKindMerge:
		patch, err := renderDeployablePatchBody(draft.BodyOrPatch, draft.TargetSkillName)
		if err != nil {
			return "", err
		}
		if !hadOriginal || strings.TrimSpace(existingBody) == "" {
			return renderDeployableSkillBody(draft.BodyOrPatch), nil
		}
		mergedSection := strings.Join([]string{
			"",
			"## Merged Knowledge",
			strings.TrimSpace(patch),
			"",
		}, "\n")
		return strings.TrimRight(existingBody, "\n") + mergedSection, nil
	default:
		return "", fmt.Errorf("unsupported change_kind %q", draft.ChangeKind)
	}
}

func renderDeployablePatchBody(body, targetSkillName string) (string, error) {
	body = renderDeployableSkillBody(body)
	frontmatter, markdownBody := splitSkillFrontmatter(body)
	if frontmatter == "" {
		markdownBody = body
	} else {
		fields, err := parseSkillFrontmatterFields(frontmatter, true)
		if err != nil {
			return "", err
		}
		if name := strings.TrimSpace(fields["name"]); name != "" && name != targetSkillName {
			return "", fmt.Errorf(
				"skill patch frontmatter name %q does not match target skill %q",
				name,
				targetSkillName,
			)
		}
	}
	return strings.TrimSpace(stripLeadingH1(markdownBody)), nil
}

func splitSkillFrontmatter(body string) (frontmatter, markdownBody string) {
	normalized := strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", body
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return "", body
	}
	return strings.Join(lines[1:end], "\n"), strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
}

func parseSkillFrontmatterFields(frontmatter string, allowExtraFields bool) (map[string]string, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return nil, fmt.Errorf("invalid skill frontmatter: %w", err)
	}
	for key := range raw {
		if key != "name" && key != "description" {
			if allowExtraFields {
				continue
			}
			return nil, fmt.Errorf("unsupported skill frontmatter field %q", key)
		}
	}

	var typed struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &typed); err != nil {
		return nil, fmt.Errorf("invalid skill frontmatter: %w", err)
	}
	return map[string]string{
		"name":        typed.Name,
		"description": typed.Description,
	}, nil
}

func stripLeadingH1(body string) string {
	lines := strings.Split(strings.TrimLeft(body, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		lines = lines[1:]
	}
	return strings.Join(lines, "\n")
}

func errorsJoin(errs ...error) error {
	var first error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if first == nil {
			first = err
			continue
		}
		first = fmt.Errorf("%w; %v", first, err)
	}
	return first
}
