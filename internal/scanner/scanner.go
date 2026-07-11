// Package scanner walks the Limitless infrastructure layer and emits a
// per-component snapshot of substrate language, R174 5-of-5 cohort
// package presence, load-bearing wire-in status, and KAT-1 byte-
// identity pin.
//
// The scanner is read-only: it never mutates the source trees. All
// detection is filesystem-pattern + grep-style. No build, no go list,
// no network — so it is safe to run from any CI runner and reproducible
// across operator machines.
package scanner

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KAT1HexCanonical is the cohort-canonical KAT-1 HMAC-SHA256 digest
// hex. Any file under an infra root that contains this literal counts
// as a KAT-1 byte-identity pin (regulator-reproducible offline via
// `openssl dgst -sha256 -mac hmac -macopt key:`).
const KAT1HexCanonical = "239a7d0d3f1bbe3a98aede01e2ad818c2db60b7177c02e2f015035b2b5b7dbca"

// CohortPackages are the five R174 cohort-maturity packages every
// infra component is expected to host once Wave-2 uplift completes.
// Names taken from the recall/pkg/* canonical reference.
var CohortPackages = [5]string{
	"mirrormark",
	"honest",
	"legal",
	"manifest",
	"firewall",
}

// Layer is the high-level grouping a scanned component belongs to.
type Layer string

const (
	LayerInfrastructure Layer = "infrastructure"
	LayerEngine         Layer = "engine"
	LayerFoundation     Layer = "foundation"
)

// Component is a single scanned infra root.
//
// PackageStatus is a length-5 array aligned with CohortPackages — the
// fixed-length array keeps the on-disk manifest schema stable when new
// cohort packages join.
type Component struct {
	Name          string   `yaml:"name" json:"name"`
	Layer         Layer    `yaml:"layer" json:"layer"`
	Path          string   `yaml:"path" json:"path"`
	Substrate     string   `yaml:"substrate" json:"substrate"`
	GoModule      string   `yaml:"go_module,omitempty" json:"go_module,omitempty"`
	PackageStatus [5]bool  `yaml:"package_status" json:"package_status"`
	CohortCount   int      `yaml:"cohort_count" json:"cohort_count"`
	LoadBearing   bool     `yaml:"load_bearing" json:"load_bearing"`
	KAT1Pinned    bool     `yaml:"kat1_pinned" json:"kat1_pinned"`
	InternalDeps  []string `yaml:"internal_deps,omitempty" json:"internal_deps,omitempty"`
	Notes         []string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// Snapshot is the full scanner output.
type Snapshot struct {
	GeneratedAt string      `yaml:"generated_at" json:"generated_at"`
	Components  []Component `yaml:"components" json:"components"`
}

// Roots groups the three scan roots used by ScanAll. Caller can
// override for test fixtures.
type Roots struct {
	InfrastructureDir string
	EnginesDir        string
	FoundationDir     string // single root with subdir components (e.g. aicore)
}

// DefaultRoots returns the canonical Limitless layout under C:\limitless.
func DefaultRoots() Roots {
	return Roots{
		InfrastructureDir: filepath.FromSlash("C:/limitless/infrastructure"),
		EnginesDir:        filepath.FromSlash("C:/limitless/engines"),
		FoundationDir:     filepath.FromSlash("C:/limitless/foundation/aicore"),
	}
}

// ScanAll walks the three roots and returns a sorted Snapshot.
// Components are sorted by (Layer, Name) for stable output.
func ScanAll(r Roots) (Snapshot, error) {
	var comps []Component

	if r.InfrastructureDir != "" {
		entries, err := readSubdirs(r.InfrastructureDir)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read infra dir: %w", err)
		}
		for _, name := range entries {
			path := filepath.Join(r.InfrastructureDir, name)
			c := scanOne(name, path, LayerInfrastructure)
			comps = append(comps, c)
		}
	}

	if r.EnginesDir != "" {
		entries, err := readSubdirs(r.EnginesDir)
		if err != nil {
			return Snapshot{}, fmt.Errorf("read engines dir: %w", err)
		}
		for _, name := range entries {
			path := filepath.Join(r.EnginesDir, name)
			c := scanOne(name, path, LayerEngine)
			comps = append(comps, c)
		}
	}

	if r.FoundationDir != "" {
		// foundation/aicore is one go.mod root — treat the whole tree
		// as a single component.
		path := r.FoundationDir
		name := filepath.Base(path)
		if exists(path) {
			c := scanOne(name, path, LayerFoundation)
			comps = append(comps, c)
		}
	}

	sort.Slice(comps, func(i, j int) bool {
		if comps[i].Layer != comps[j].Layer {
			return comps[i].Layer < comps[j].Layer
		}
		return comps[i].Name < comps[j].Name
	})

	return Snapshot{Components: comps}, nil
}

// scanOne builds a Component for the single tree rooted at path.
func scanOne(name, path string, layer Layer) Component {
	c := Component{
		Name:  name,
		Layer: layer,
		Path:  path,
	}
	c.Substrate, c.GoModule = detectSubstrate(path)
	c.PackageStatus, c.CohortCount = detectCohortPackages(path)
	c.LoadBearing = detectLoadBearing(path)
	c.KAT1Pinned = detectKAT1Pin(path)
	c.InternalDeps = detectInternalDeps(path, name)
	if c.CohortCount == 0 && layer != LayerFoundation {
		c.Notes = append(c.Notes, "no R174 cohort packages present")
	}
	if c.CohortCount == 5 && !c.LoadBearing {
		c.Notes = append(c.Notes, "5-of-5 present but not load-bearing")
	}
	c.Notes = append(c.Notes, testOnlyCohortNotes(path, c.PackageStatus)...)
	return c
}

// detectSubstrate returns ("go", "<module-path>") for a Go-rooted
// tree, ("python", "") for a Python project, etc. The detection is
// best-effort and intentionally simple — anything ambiguous falls
// back to "unknown".
func detectSubstrate(path string) (substrate, mod string) {
	// 1. Go: go.mod at root, or a single nested go.mod (nexus pattern).
	if exists(filepath.Join(path, "go.mod")) {
		mod = readModulePath(filepath.Join(path, "go.mod"))
		return "go", mod
	}
	// Walk one level for nested go.mod (e.g. nexus has src/api/go.mod).
	nested := findNearestGoMod(path, 3)
	if nested != "" {
		mod = readModulePath(nested)
		return "go", mod
	}
	// 2. Python.
	if exists(filepath.Join(path, "pyproject.toml")) || exists(filepath.Join(path, "setup.py")) {
		return "python", ""
	}
	// 3. Rust.
	if exists(filepath.Join(path, "Cargo.toml")) {
		return "rust", ""
	}
	// 4. Crystal.
	if exists(filepath.Join(path, "shard.yml")) {
		return "crystal", ""
	}
	// 5. Node / TS.
	if exists(filepath.Join(path, "package.json")) {
		// tsconfig.json hint → typescript
		if exists(filepath.Join(path, "tsconfig.json")) {
			return "typescript", ""
		}
		return "javascript", ""
	}
	// 6. C.
	if exists(filepath.Join(path, "Makefile")) && hasHeaderOrSource(path, []string{".c", ".h"}) {
		return "c", ""
	}
	// 7. Elixir / BEAM.
	if exists(filepath.Join(path, "mix.exs")) {
		return "elixir", ""
	}
	return "unknown", ""
}

// findNearestGoMod walks at most maxDepth levels below path looking
// for a go.mod. Returns first hit or "".
func findNearestGoMod(path string, maxDepth int) string {
	var found string
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(path, p)
		depth := strings.Count(rel, string(os.PathSeparator))
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && d.Name() == "go.mod" {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// readModulePath reads the first `module <path>` line from go.mod.
func readModulePath(goMod string) string {
	b, err := os.ReadFile(goMod)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func hasHeaderOrSource(path string, exts []string) bool {
	var ok bool
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		for _, e := range exts {
			if strings.HasSuffix(d.Name(), e) {
				ok = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return ok
}

// detectCohortPackages checks for the presence of the five R174
// cohort packages under any of the conventional locations:
//
//	<root>/internal/<pkg>/
//	<root>/pkg/<pkg>/
//	<root>/pkg/<root-name>/<pkg>/ (rare nested layout)
//
// "Presence" means a non-placeholder Go source file exists inside the
// directory — empty placeholder dirs (codex pattern) do NOT count, and
// neither does a dir holding only _test.go files (the test-only form is
// surfaced separately as a "<pkg>: test-only" note by scanOne).
func detectCohortPackages(path string) ([5]bool, int) {
	var status [5]bool
	for i, pkg := range CohortPackages {
		for _, c := range cohortCandidates(path, pkg) {
			if dirHasNonTestGoFile(c) {
				status[i] = true
				break
			}
		}
	}
	n := 0
	for _, b := range status {
		if b {
			n++
		}
	}
	return status, n
}

// cohortCandidates lists the conventional locations a cohort package
// may live at under a component root. The flat <root>/<pkg> candidate
// covers foundation/aicore, which lays packages out at the root level
// (no internal/pkg prefix).
func cohortCandidates(path, pkg string) []string {
	return []string{
		filepath.Join(path, "internal", pkg),
		filepath.Join(path, "pkg", pkg),
		filepath.Join(path, pkg),
	}
}

// dirHasNonTestGoFile returns true if dir contains at least one .go
// file that is not a _test.go file. Empty placeholders return false.
func dirHasNonTestGoFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
	}
	return false
}

// dirHasTestGoFile returns true if dir contains at least one _test.go
// file (regardless of whether non-test files are present).
func dirHasTestGoFile(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			return true
		}
	}
	return false
}

// testOnlyCohortNotes returns a deterministic note per cohort package
// that was counted ABSENT (per status) but whose conventional dirs
// hold at least one _test.go file — i.e. the package exists in a
// test-only form (echo's firewall pattern: R145.C pins carried in a
// pure-test package).
//
// The counting convention is deliberately unchanged — a test-only
// package still counts absent, matching dirHasNonTestGoFile — but the
// note makes a 4-of-5 explainable instead of silently conflating
// "test-only" with "missing".
func testOnlyCohortNotes(path string, status [5]bool) []string {
	var notes []string
	for i, pkg := range CohortPackages {
		if status[i] {
			continue
		}
		for _, c := range cohortCandidates(path, pkg) {
			if dirHasTestGoFile(c) {
				notes = append(notes, pkg+": test-only")
				break
			}
		}
	}
	return notes
}

// detectLoadBearing returns true when *any* non-test Go file under
// path contains a call expression matching one of:
//
//	mirrormark.Sign(
//	marker.Sign(
//	MirrorMark.Sign(
//
// Markers live in production code — if found only inside _test.go
// files we report false (decorative).
func detectLoadBearing(path string) bool {
	needles := [][]byte{
		[]byte("mirrormark.Sign("),
		[]byte("marker.Sign("),
		[]byte("MirrorMark.Sign("),
	}
	var found bool
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" || n == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		// Cap the read at 1MB to match detectKAT1Pin's guard — Sign
		// call-sites live in ordinary source files, never in huge
		// generated/blob files, so an unbounded ReadFile is needless
		// memory pressure.
		fi, err := os.Stat(p)
		if err != nil || fi.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, n := range needles {
			if bytes.Contains(b, n) {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

// detectKAT1Pin returns true when any file under path contains the
// canonical KAT-1 hex literal.
func detectKAT1Pin(path string) bool {
	needle := []byte(KAT1HexCanonical)
	var found bool
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip large binary-ish files (>1MB) — not where pins live.
		fi, err := os.Stat(p)
		if err != nil || fi.Size() > 1<<20 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if bytes.Contains(b, needle) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// detectInternalDeps lists infra-component names referenced as
// `internal/<name>` subdirs under path. This is how we surface
// cross-infra fan-out (e.g. an engine depending on a service stub).
//
// selfName is the scanned component's own name: a component commonly
// hosts an `internal/<own-name>` package for its local types (the
// delve pattern — internal/delve holds delve's error/type defs), which
// is NOT a dependency edge, so the component's own name is excluded.
func detectInternalDeps(path, selfName string) []string {
	internalDir := filepath.Join(path, "internal")
	entries, err := os.ReadDir(internalDir)
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, k := range knownInfraNames() {
		known[k] = true
	}
	var deps []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if e.Name() == selfName {
			continue
		}
		if known[e.Name()] {
			deps = append(deps, e.Name())
		}
	}
	sort.Strings(deps)
	return deps
}

// knownInfraNames is the canonical list of infrastructure component
// names. It is kept inline so the scanner has zero filesystem
// dependencies beyond the trees it scans. Names match the on-disk
// directory names under C:/limitless/infrastructure/.
func knownInfraNames() []string {
	return []string{
		"chronicle", "codex", "conduit", "corroboration",
		"crucible-bridge", "delve", "escape-service",
		"forge-central", "forge-registry", "gauntlet",
		"grounded", "ingest", "kiln", "limitless-c-crypto",
		"lore", "membrane-service", "mint", "muse", "nexus",
		"pennant", "phantom", "piper", "recall", "reservoir",
		"schema", "sentinel", "shadow-service", "shuttle",
		"spyglass", "switchyard", "toolforge-service", "vault",
		// engines
		"causal", "echo", "oracle", "parallax", "synthesis",
		// foundation
		"aicore",
	}
}

// readSubdirs returns the names of immediate-child directories
// (excluding hidden and dotfiles).
func readSubdirs(parent string) ([]string, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
