// Package moddeps parses go.mod files across the Limitless layer trees to
// produce a RECEIPT-grade module dependency edge: which repos actually
// `require` (or `replace =>` the local path of) each producer component's
// Go module.
//
// This is the deterministic counterpart to relate.CountConsumers, which is
// a SoftProxy directory-name census (R174 cohort-pattern adoption). The
// census answers "which flagships host a same-named cohort-pattern dir"; it
// is not a dependency graph and, per the fidelity audit, inverts real
// dependency degree — aicore is required by 30+ modules yet hosts no
// same-named pattern dir (census 0), while lore is pattern-vendored into
// ~53 flagships but required by exactly one real module (nexus, census 53).
//
// moddeps parses the build manifest itself, so aicore is finally counted as
// the high-degree hub it is and lore at its true module degree of 1. The two
// signals are emitted side by side (consumer_count vs module_consumer_count)
// and labelled distinctly so a caller never mistakes the proxy for the receipt.
//
// The parse is pure filesystem + text: no `go list`, no build, no network —
// so it stays as reproducible and CI-safe as the rest of the tool.
package moddeps

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SearchRoot is a layer directory to scan for consuming go.mod files.
//
// SingleComponent distinguishes the two layouts the scanner already walks:
//   - false: Dir is a PARENT whose immediate children are components
//     (infrastructure/, engines/, flagships/) — a go.mod's owning component
//     is the first path segment under Dir (so infrastructure/nexus/src/api/
//     go.mod is owned by "nexus").
//   - true: the whole tree under Dir is ONE component named filepath.Base(Dir)
//     (the foundation/aicore layout — Dir points at the component itself).
type SearchRoot struct {
	Dir             string
	SingleComponent bool
}

// Producer identifies a component whose module consumers are counted.
type Producer struct {
	Name       string // component name (snapshot directory name)
	ModulePath string // go.mod module path, e.g. github.com/davly/aicore
	RootDir    string // absolute component root dir (for replace-target resolution)
}

// skipDirs are directory names never descended into when hunting go.mod
// files. vendor/ and testdata/ are required by the spec; node_modules and
// dotdirs are excluded as a matter of course (a go.mod inside a vendored
// JS tree or a .git internal is never a first-party consumer).
var skipDirs = map[string]bool{
	"vendor":       true,
	"testdata":     true,
	"node_modules": true,
}

// CountModuleConsumers walks every SearchRoot for go.mod files, parses their
// require + replace directives, and returns producerName -> sorted unique
// consumer component names (self excluded).
//
// A consumer C is recorded for producer P when C's go.mod either:
//   - has a require directive for P's module path, OR
//   - has a replace directive whose target local path resolves to P's RootDir.
//
// Output lists are sorted and de-duplicated, so the result is byte-stable for
// a given filesystem. Producers with no consumers map to a nil slice (the key
// is always present so callers can emit a deterministic zero).
func CountModuleConsumers(roots []SearchRoot, producers []Producer) (map[string][]string, error) {
	byModule := make(map[string]string, len(producers)) // module path -> producer name
	byDir := make(map[string]string, len(producers))    // cleaned root dir -> producer name
	for _, p := range producers {
		if p.ModulePath != "" {
			byModule[p.ModulePath] = p.Name
		}
		if p.RootDir != "" {
			byDir[canonDir(p.RootDir)] = p.Name
		}
	}

	// producerName -> set of consumer names.
	sets := make(map[string]map[string]bool, len(producers))
	for _, p := range producers {
		sets[p.Name] = map[string]bool{}
	}

	for _, root := range roots {
		if root.Dir == "" {
			continue
		}
		walkRoot(root, byModule, byDir, sets)
	}

	out := make(map[string][]string, len(sets))
	for name, set := range sets {
		if len(set) == 0 {
			out[name] = nil
			continue
		}
		list := make([]string, 0, len(set))
		for c := range set {
			list = append(list, c)
		}
		sort.Strings(list)
		out[name] = list
	}
	return out, nil
}

// walkRoot descends root looking for go.mod files, recording each consuming
// edge into sets. Errors on individual entries are skipped (best-effort,
// read-only) — a mis-pointed root simply yields no edges rather than aborting.
func walkRoot(root SearchRoot, byModule, byDir map[string]string, sets map[string]map[string]bool) {
	_ = filepath.WalkDir(root.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			n := d.Name()
			if p != root.Dir && (skipDirs[n] || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		owner := ownerName(root, filepath.Dir(p))
		if owner == "" {
			return nil
		}
		required, replaceDirs := parseGoMod(p)
		for m := range required {
			if pname, ok := byModule[m]; ok && pname != owner {
				sets[pname][owner] = true
			}
		}
		for dir := range replaceDirs {
			if pname, ok := byDir[dir]; ok && pname != owner {
				sets[pname][owner] = true
			}
		}
		return nil
	})
}

// ownerName maps a go.mod's directory to the component that owns it. For a
// SingleComponent root that is base(root); otherwise it is the first path
// segment of the go.mod dir relative to the root (so a nested go.mod such as
// nexus/src/api/go.mod is attributed to "nexus"). A go.mod sitting directly
// at a parent root has no owning component and returns "".
func ownerName(root SearchRoot, gomodDir string) string {
	if root.SingleComponent {
		return filepath.Base(filepath.Clean(root.Dir))
	}
	rel, err := filepath.Rel(root.Dir, gomodDir)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") {
		return ""
	}
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return rel
}

// parseGoMod reads a go.mod and returns the set of required module paths and
// the set of canonicalised local directories that a replace directive targets
// (resolved relative to the go.mod's own directory). Comments are stripped and
// both block and single-line require/replace forms are handled. A read error
// yields empty sets (best-effort).
func parseGoMod(gomodPath string) (required map[string]bool, replaceDirs map[string]bool) {
	required = map[string]bool{}
	replaceDirs = map[string]bool{}
	b, err := os.ReadFile(gomodPath)
	if err != nil {
		return required, replaceDirs
	}
	gomodDir := filepath.Dir(gomodPath)

	const (
		blockNone = iota
		blockRequire
		blockReplace
	)
	block := blockNone

	for _, raw := range strings.Split(string(b), "\n") {
		line := stripComment(raw)
		if line == "" {
			continue
		}
		switch block {
		case blockRequire:
			if line == ")" {
				block = blockNone
				continue
			}
			if m := firstToken(line); m != "" {
				required[m] = true
			}
			continue
		case blockReplace:
			if line == ")" {
				block = blockNone
				continue
			}
			addReplace(line, gomodDir, replaceDirs)
			continue
		}

		// block == blockNone: look for directive starts.
		switch {
		case line == "require (" || line == "require(":
			block = blockRequire
		case line == "replace (" || line == "replace(":
			block = blockReplace
		case strings.HasPrefix(line, "require "):
			if m := firstToken(strings.TrimSpace(line[len("require "):])); m != "" {
				required[m] = true
			}
		case strings.HasPrefix(line, "replace "):
			addReplace(strings.TrimSpace(line[len("replace "):]), gomodDir, replaceDirs)
		}
	}
	return required, replaceDirs
}

// addReplace parses a single replace entry body ("LHS [ver] => RHS [ver]") and,
// when the RHS is a local filesystem path, records the resolved directory.
func addReplace(body, gomodDir string, replaceDirs map[string]bool) {
	arrow := strings.Index(body, "=>")
	if arrow < 0 {
		return
	}
	rhs := strings.TrimSpace(body[arrow+2:])
	target := firstToken(rhs)
	if target == "" || !isLocalPath(target) {
		return
	}
	resolved := filepath.Join(gomodDir, filepath.FromSlash(target))
	replaceDirs[canonDir(resolved)] = true
}

// isLocalPath reports whether a replace target is a filesystem path (as
// opposed to a module-path replacement). go.mod local targets always begin
// with "./", "../", or are absolute; a bare module path (no leading dot,
// no drive/root) is a module replacement and does not resolve to a dir.
func isLocalPath(p string) bool {
	if p == "." || p == ".." {
		return true
	}
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") ||
		strings.HasPrefix(p, ".\\") || strings.HasPrefix(p, "..\\") {
		return true
	}
	return filepath.IsAbs(p)
}

// canonDir normalises a directory path for comparison: cleaned, forward-slash,
// lower-cased (Windows paths are case-insensitive and the estate lives on a
// single case-insensitive volume, so folding avoids drive-letter/case drift).
func canonDir(p string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(p)))
}

// stripComment removes a trailing `//` line comment and surrounding
// whitespace. go.mod has no string literals, so a bare `//` scan is safe.
func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// firstToken returns the first whitespace-delimited token of s (or "").
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexFunc(s, func(r rune) bool { return r == ' ' || r == '\t' }); i >= 0 {
		return s[:i]
	}
	return s
}
