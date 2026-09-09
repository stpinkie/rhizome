// Package skills — bundle support for mesh skill distribution: a shareable
// skill directory is packed into a zip archive, transferred over the blob
// protocol, and unpacked under the receiver's global skills root with
// origin metadata.
package skills

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
	"github.com/stpinkie/rhizome/pkg/guard"
)

// SkillOriginFile is the metadata marker written into an installed skill
// directory recording where the skill came from.
const SkillOriginFile = ".skill-origin.json"

// maxSkillBundleBytes bounds a skill bundle (default blob cap is larger;
// skills are documentation + small scripts).
const maxSkillBundleBytes = 8 << 20

// maxSkillFiles bounds the number of files inside a bundle.
const maxSkillFiles = 256

// PackSkillDir zips the skill directory (the directory containing SKILL.md)
// into destZip. Returns an error when the directory is not a skill.
func PackSkillDir(dir, destZip string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("skill directory not found: %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return fmt.Errorf("not a skill directory (no SKILL.md): %s", dir)
	}

	f, err := os.Create(destZip)
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	return filepath.WalkDir(dir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." || d.IsDir() {
			return nil
		}
		// Skip VCS and origin metadata.
		base := filepath.Base(path)
		if base == SkillOriginFile || strings.HasPrefix(rel, ".git") {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			return ferr
		}
		if fi.Size() > maxSkillBundleBytes {
			return fmt.Errorf("skill file too large: %s (%d bytes)", rel, fi.Size())
		}
		w, werr := zw.Create(filepath.ToSlash(rel))
		if werr != nil {
			return werr
		}
		src, serr := os.Open(path)
		if serr != nil {
			return serr
		}
		_, cerr := io.Copy(w, src)
		_ = src.Close()
		return cerr
	})
}

// ScanResult reports what the guard scan found inside an unpacked bundle.
type ScanResult struct {
	Suspicious bool     `json:"suspicious"`
	Matches    []string `json:"matches,omitempty"`
}

// ScanSkillDir runs prompt-injection heuristics over text-like files in an
// unpacked skill directory. Scripts and markdown are the carrier for
// injected instructions, so both are scanned.
func ScanSkillDir(dir string) ScanResult {
	var res ScanResult
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".md", ".txt", ".sh", ".ps1", ".py", ".js", ".ts", ".yaml", ".yml", ".json", "":
		default:
			return nil // binary-ish files are not instruction carriers
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if len(data) > maxSkillBundleBytes {
			return nil
		}
		if match, found := guard.ContainsPromptInjection(string(data)); found {
			res.Suspicious = true
			res.Matches = append(res.Matches,
				fmt.Sprintf("%s: %s", filepath.Base(path), match))
		}
		return nil
	})
	sort.Strings(res.Matches)
	return res
}

// SkillOriginMeta is written to <skill-dir>/.skill-origin.json.
type SkillOriginMeta struct {
	Version          int    `json:"version"`
	OriginKind       string `json:"origin_kind"`
	Registry         string `json:"registry"`
	InstalledVersion string `json:"installed_version,omitempty"`
	InstalledAt      int64  `json:"installed_at"`
}

// WriteSkillOrigin records provenance for an installed skill directory.
func WriteSkillOrigin(dir, originKind, registry string) error {
	meta := SkillOriginMeta{
		Version:     1,
		OriginKind:  originKind,
		Registry:    registry,
		InstalledAt: time.Now().UnixMilli(),
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, SkillOriginFile), data, 0o600)
}

// UnpackSkillBundle extracts a packed skill zip into destDir. The archive
// must contain SKILL.md at its root; path traversal and oversized entries
// are rejected. Returns the guard scan result over the extracted files.
func UnpackSkillBundle(zipPath, name, destDir string) (ScanResult, error) {
	if err := ValidateSkillName(name); err != nil {
		return ScanResult{}, err
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ScanResult{}, fmt.Errorf("open bundle: %w", err)
	}
	defer zr.Close()

	cleanDest := filepath.Clean(destDir) + string(filepath.Separator)
	if len(zr.File) > maxSkillFiles {
		return ScanResult{}, fmt.Errorf("bundle has too many files (%d)", len(zr.File))
	}

	hasSkillMD := false
	for _, f := range zr.File {
		entry := filepath.FromSlash(f.Name)
		if filepath.IsAbs(entry) || strings.HasPrefix(entry, "..") {
			return ScanResult{}, fmt.Errorf("unsafe bundle path: %s", f.Name)
		}
		target := filepath.Join(destDir, entry)
		if !strings.HasPrefix(filepath.Clean(target)+string(filepath.Separator), cleanDest) {
			return ScanResult{}, fmt.Errorf("bundle path escapes target: %s", f.Name)
		}
		if f.UncompressedSize64 > maxSkillBundleBytes {
			return ScanResult{}, fmt.Errorf("bundle file too large: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.EqualFold(f.Name, "SKILL.md") {
			hasSkillMD = true
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ScanResult{}, err
		}
		rc, rerr := f.Open()
		if rerr != nil {
			return ScanResult{}, rerr
		}
		data, rerr := io.ReadAll(io.LimitReader(rc, maxSkillBundleBytes+1))
		_ = rc.Close()
		if rerr != nil {
			return ScanResult{}, rerr
		}
		if int64(len(data)) > maxSkillBundleBytes {
			return ScanResult{}, fmt.Errorf("bundle file too large: %s", f.Name)
		}
		if werr := os.WriteFile(target, data, 0o644); werr != nil {
			return ScanResult{}, werr
		}
	}
	if !hasSkillMD {
		return ScanResult{}, fmt.Errorf("bundle is missing SKILL.md")
	}
	return ScanSkillDir(destDir), nil
}
