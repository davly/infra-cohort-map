// Package relate walks the Limitless flagships layer counting which
// flagships consume each infrastructure component.
//
// "Consumes" is detected by the presence of any of the following
// subdirectories inside a flagship root:
//
//	flagships/<flagship>/internal/<infra>/
//	flagships/<flagship>/<dialect>/internal/<infra>/   (kotlin pattern)
//	flagships/<flagship>/<dialect>/<infra>/            (snake-cased)
//	flagships/<flagship>/src/<infra>/                  (rust/swift pattern)
//
// We deliberately stop at the *directory* signal. A grep-based check
// (e.g. importing the package) catches more but is fragile across the
// ~38 substrate languages in the L43 cohort — and the directory
// signal is what the cohort review framework already documents.
//
// A hit directory must, however, contain at least one non-test source
// file (any substrate, anywhere under it) — an empty placeholder dir
// (codex pattern) is not consumption. This mirrors the scanner's own
// cohort-package placeholder guard (scanner.dirHasNonTestGoFile).
package relate

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Consumers maps infra-component-name → sorted slice of flagship
// names that depend on it.
type Consumers map[string][]string

// CountConsumers walks flagshipsDir once and returns the consumer
// map.  infraNames is the canonical list of infra component names to
// search for — components not in this list will be missed even if
// they exist on disk.  Passing nil uses a built-in default that
// matches scanner.knownInfraNames().
func CountConsumers(flagshipsDir string, infraNames []string) (Consumers, error) {
	if len(infraNames) == 0 {
		infraNames = DefaultInfraNames()
	}
	known := make(map[string]bool, len(infraNames))
	for _, n := range infraNames {
		known[n] = true
	}

	entries, err := os.ReadDir(flagshipsDir)
	if err != nil {
		return nil, err
	}

	c := make(Consumers)
	for _, name := range infraNames {
		c[name] = nil
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		flagship := e.Name()
		if strings.HasPrefix(flagship, ".") {
			continue
		}
		root := filepath.Join(flagshipsDir, flagship)
		hits := scanFlagship(root, known)
		for infra := range hits {
			c[infra] = append(c[infra], flagship)
		}
	}

	for k := range c {
		sort.Strings(c[k])
		c[k] = dedupe(c[k])
	}
	return c, nil
}

// anchorParents are the directory names whose *immediate children* may
// legitimately be infra-component dirs, per the documented consumption
// patterns:
//
//	<flagship>/internal/<infra>/
//	<flagship>/src/<infra>/
//	<flagship>/pkg/<infra>/
//	<flagship>/<dialect>/<infra>/            (snake-cased / kotlin pattern)
//	<flagship>/<dialect>/internal/<infra>/   (still parented by internal)
//
// Requiring a hit to be parented by one of these stops generic
// directory names that happen to collide with an infra-component name
// (schema / echo / oracle / lore — common as GraphQL-schema, web-
// framework, db-connector, or content dirs) from being mis-counted as
// real infra consumption. A bare `<flagship>/<dialect>/graphql/schema`
// is no longer a "schema consumer"; `<flagship>/internal/schema` still
// is.
var anchorParents = map[string]bool{
	"internal": true,
	"src":      true,
	"pkg":      true,
	// language/dialect subdirs — the snake-cased pattern parents the
	// infra dir directly by the dialect name.
	"go": true, "rust": true, "python": true, "py": true,
	"kotlin": true, "swift": true, "java": true, "scala": true,
	"typescript": true, "ts": true, "javascript": true, "js": true,
	"node": true, "crystal": true, "elixir": true, "c": true,
	"cpp": true, "ruby": true, "rb": true, "csharp": true, "cs": true,
	"zig": true, "dart": true, "php": true,
}

// scanFlagship walks a single flagship root to a bounded depth
// looking for directories named after a known infra component whose
// parent dir is an anchor (see anchorParents) and which contain at
// least one non-test source file (empty placeholder dirs don't count).
func scanFlagship(root string, known map[string]bool) map[string]bool {
	hits := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "target" || name == "build" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > 4 {
			return filepath.SkipDir
		}
		if known[name] && anchorParents[filepath.Base(filepath.Dir(p))] && dirHasNonTestSourceFile(p) {
			hits[name] = true
		}
		return nil
	})
	return hits
}

// sourceExts are the file extensions recognised as source code across
// the cohort's substrate languages (mirrors the substrates the scanner
// detects plus the dialect anchors above). A consumption hit must be
// backed by at least one such file — docs, fixtures, or an empty
// placeholder dir are not consumption.
var sourceExts = map[string]bool{
	".go": true, ".rs": true, ".py": true,
	".kt": true, ".kts": true, ".swift": true,
	".java": true, ".scala": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true,
	".cr": true, ".ex": true, ".exs": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true,
	".hpp": true, ".hh": true,
	".rb": true, ".cs": true, ".zig": true, ".dart": true,
	".php": true,
}

// isNonTestSourceFile reports whether name looks like a production
// source file: a recognised source extension that does not follow a
// test-file naming convention (`*_test.<ext>` Go/pytest-adjacent,
// `test_*` pytest, `*_spec.<ext>` rspec, `*.test.<ext>` / `*.spec.<ext>`
// JS/TS).
func isNonTestSourceFile(name string) bool {
	lower := strings.ToLower(name)
	ext := filepath.Ext(lower)
	if !sourceExts[ext] {
		return false
	}
	stem := strings.TrimSuffix(lower, ext)
	if strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_spec") {
		return false
	}
	if strings.HasPrefix(lower, "test_") {
		return false
	}
	if strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") {
		return false
	}
	return true
}

// dirHasNonTestSourceFile reports whether dir contains at least one
// non-test source file anywhere under it (skipping the same dirs the
// flagship walk skips). Empty placeholder dirs — and dirs holding only
// docs/fixtures or test files — return false, mirroring the scanner's
// cohort-package placeholder guard.
func dirHasNonTestSourceFile(dir string) bool {
	var found bool
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" || n == "target" || n == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		if isNonTestSourceFile(d.Name()) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// CountSummary returns name → count for a Consumers map, sorted by
// descending count then name.
type Pair struct {
	Name  string
	Count int
}

func (c Consumers) Pairs() []Pair {
	var out []Pair
	for k, v := range c {
		out = append(out, Pair{Name: k, Count: len(v)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DefaultFlagshipsDir is the canonical Limitless flagships dir.
func DefaultFlagshipsDir() string {
	return filepath.FromSlash("C:/limitless/flagships")
}

// DefaultInfraNames mirrors scanner.knownInfraNames() — kept inline
// to avoid circular import with the scanner package.
func DefaultInfraNames() []string {
	return []string{
		"chronicle", "codex", "conduit", "corroboration",
		"crucible-bridge", "delve", "escape-service",
		"forge-central", "forge-registry", "gauntlet",
		"grounded", "ingest", "kiln", "limitless-c-crypto",
		"lore", "membrane-service", "mint", "muse", "nexus",
		"pennant", "phantom", "piper", "recall", "reservoir",
		"schema", "sentinel", "shadow-service", "shuttle",
		"spyglass", "switchyard", "toolforge-service", "vault",
		"causal", "echo", "oracle", "parallax", "synthesis",
		"aicore",
	}
}

func dedupe(xs []string) []string {
	if len(xs) == 0 {
		return xs
	}
	out := xs[:0]
	prev := ""
	for i, s := range xs {
		if i == 0 || s != prev {
			out = append(out, s)
		}
		prev = s
	}
	return out
}
