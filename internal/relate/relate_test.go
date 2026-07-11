package relate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func scaffoldFlagship(t *testing.T, parent, name string, internalSubdirs []string) {
	t.Helper()
	flagshipRoot := filepath.Join(parent, name)
	for _, sub := range internalSubdirs {
		writeFile(t, filepath.Join(flagshipRoot, "internal", sub), "x.go",
			"package "+sanitize(sub)+"\n")
	}
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "x"
	}
	return string(out)
}

func TestCountConsumers_BasicCounts(t *testing.T) {
	tmp := t.TempDir()
	scaffoldFlagship(t, tmp, "alpha", []string{"recall", "codex"})
	scaffoldFlagship(t, tmp, "bravo", []string{"recall"})
	scaffoldFlagship(t, tmp, "charlie", []string{"chronicle"})

	c, err := CountConsumers(tmp, []string{"recall", "codex", "chronicle"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 2 {
		t.Fatalf("recall consumers: got %v want 2", c["recall"])
	}
	if len(c["codex"]) != 1 || c["codex"][0] != "alpha" {
		t.Fatalf("codex consumers: got %v", c["codex"])
	}
	if len(c["chronicle"]) != 1 || c["chronicle"][0] != "charlie" {
		t.Fatalf("chronicle consumers: got %v", c["chronicle"])
	}
}

func TestCountConsumers_UnknownNamesIgnored(t *testing.T) {
	tmp := t.TempDir()
	scaffoldFlagship(t, tmp, "alpha", []string{"recall", "totally-unrelated-thing"})
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c) != 1 {
		t.Fatalf("expected single entry: got %d keys", len(c))
	}
	if _, ok := c["totally-unrelated-thing"]; ok {
		t.Fatal("unknown name leaked into map")
	}
}

func TestCountConsumers_PairsSortDescByCount(t *testing.T) {
	tmp := t.TempDir()
	scaffoldFlagship(t, tmp, "alpha", []string{"recall", "codex"})
	scaffoldFlagship(t, tmp, "bravo", []string{"recall"})
	scaffoldFlagship(t, tmp, "charlie", []string{"recall"})
	scaffoldFlagship(t, tmp, "delta", []string{"chronicle"})

	c, err := CountConsumers(tmp, []string{"recall", "codex", "chronicle"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	pairs := c.Pairs()
	if len(pairs) != 3 {
		t.Fatalf("pairs: got %d want 3", len(pairs))
	}
	if pairs[0].Name != "recall" || pairs[0].Count != 3 {
		t.Errorf("first pair: got %+v want {recall 3}", pairs[0])
	}
	// codex and chronicle both have count=1; alphabetical fallback.
	if pairs[1].Name != "chronicle" {
		t.Errorf("second pair: got %+v want chronicle", pairs[1])
	}
	if pairs[2].Name != "codex" {
		t.Errorf("third pair: got %+v want codex", pairs[2])
	}
}

func TestCountConsumers_DeepNestedHits(t *testing.T) {
	tmp := t.TempDir()
	// flagships sometimes nest the dialect dir then internal — test it.
	flagshipRoot := filepath.Join(tmp, "alpha")
	writeFile(t, filepath.Join(flagshipRoot, "alpha-core", "internal", "recall"), "x.go", "package recall\n")
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 1 {
		t.Fatalf("recall consumers nested: got %v want [alpha]", c["recall"])
	}
}

func TestCountConsumers_DedupesSameFlagshipTwice(t *testing.T) {
	tmp := t.TempDir()
	// Two paths in one flagship pointing to same infra name —
	// shouldn't count the flagship twice.
	flagshipRoot := filepath.Join(tmp, "alpha")
	writeFile(t, filepath.Join(flagshipRoot, "internal", "recall"), "x.go", "package recall\n")
	writeFile(t, filepath.Join(flagshipRoot, "src", "recall"), "x.go", "package recall\n")
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 1 {
		t.Fatalf("dedupe: got %v want [alpha]", c["recall"])
	}
}

func TestCountConsumers_GenericDirNotCounted(t *testing.T) {
	tmp := t.TempDir()
	// "schema" is a known infra name but here it is a generic GraphQL
	// schema dir nested under a non-anchor parent ("graphql") — it must
	// NOT be counted as infra consumption. Before the anchor fix this
	// over-counted to [alpha].
	writeFile(t, filepath.Join(tmp, "alpha", "graphql", "schema"), "x.go", "package schema\n")
	// "lore" sitting under an anchor parent (internal) DOES count.
	writeFile(t, filepath.Join(tmp, "alpha", "internal", "lore"), "x.go", "package lore\n")
	c, err := CountConsumers(tmp, []string{"schema", "lore"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["schema"]) != 0 {
		t.Fatalf("generic schema dir over-counted: got %v want []", c["schema"])
	}
	if len(c["lore"]) != 1 || c["lore"][0] != "alpha" {
		t.Fatalf("lore under internal: got %v want [alpha]", c["lore"])
	}
}

func TestCountConsumers_DialectParentAnchored(t *testing.T) {
	tmp := t.TempDir()
	// kotlin snake-cased pattern: <flagship>/<dialect>/<infra>/
	writeFile(t, filepath.Join(tmp, "alpha", "kotlin", "recall"), "x.go", "package recall\n")
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 1 || c["recall"][0] != "alpha" {
		t.Fatalf("dialect-anchored recall: got %v want [alpha]", c["recall"])
	}
}

func TestDefaultInfraNames_NonEmpty(t *testing.T) {
	n := DefaultInfraNames()
	if len(n) < 10 {
		t.Fatalf("DefaultInfraNames: got %d want >=10", len(n))
	}
	var sawNexus, sawRecall, sawAicore bool
	for _, name := range n {
		switch name {
		case "nexus":
			sawNexus = true
		case "recall":
			sawRecall = true
		case "aicore":
			sawAicore = true
		}
	}
	if !sawNexus || !sawRecall || !sawAicore {
		t.Errorf("expected nexus/recall/aicore in defaults: nexus=%v recall=%v aicore=%v", sawNexus, sawRecall, sawAicore)
	}
}

// mkDir creates an (empty) directory tree, failing the test on error.
func mkDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func TestCountConsumers_EmptyPlaceholderDirNotCounted(t *testing.T) {
	tmp := t.TempDir()
	// An empty flagship dir named after an infra component under an
	// anchor parent is a placeholder (codex pattern), not consumption —
	// mirrors the scanner's cohort-package placeholder guard.
	mkDir(t, filepath.Join(tmp, "alpha", "internal", "recall"))
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 0 {
		t.Fatalf("empty placeholder dir counted as consumer: got %v want []", c["recall"])
	}
}

func TestCountConsumers_NonSourceFilesOnlyNotCounted(t *testing.T) {
	tmp := t.TempDir()
	// Docs/fixtures alone are not consumption.
	writeFile(t, filepath.Join(tmp, "alpha", "go", "lore"), "notes.txt", "not source\n")
	c, err := CountConsumers(tmp, []string{"lore"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["lore"]) != 0 {
		t.Fatalf("doc-only dir counted as consumer: got %v want []", c["lore"])
	}
}

func TestCountConsumers_TestOnlyFilesNotCounted(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "alpha", "internal", "recall"), "x_test.go", "package recall\n")
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 0 {
		t.Fatalf("test-only dir counted as consumer: got %v want []", c["recall"])
	}
}

func TestCountConsumers_NestedSourceFileStillCounts(t *testing.T) {
	tmp := t.TempDir()
	// The source file may live in a subpackage of the hit dir — the
	// guard is recursive, only genuinely file-less trees are excluded.
	writeFile(t, filepath.Join(tmp, "alpha", "internal", "recall", "sub"), "x.go", "package sub\n")
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 1 || c["recall"][0] != "alpha" {
		t.Fatalf("nested source file: got %v want [alpha]", c["recall"])
	}
}

func TestCountConsumers_NonGoSubstrateSourceCounts(t *testing.T) {
	tmp := t.TempDir()
	// Multi-substrate flagships stay counted: a Rust file under the
	// src anchor is a source-backed hit.
	writeFile(t, filepath.Join(tmp, "alpha", "src", "lore"), "y.rs", "pub fn y() {}\n")
	c, err := CountConsumers(tmp, []string{"lore"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["lore"]) != 1 || c["lore"][0] != "alpha" {
		t.Fatalf("rust source hit: got %v want [alpha]", c["lore"])
	}
}

func TestIsNonTestSourceFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"x.go", true},
		{"y.rs", true},
		{"main.kt", true},
		{"app.py", true},
		{"x_test.go", false},
		{"test_x.py", false},
		{"x_spec.rb", false},
		{"x.test.ts", false},
		{"x.spec.js", false},
		{"notes.txt", false},
		{"README.md", false},
		{"kat1.hex", false},
		{"forge.graphql", false},
	}
	for _, tc := range cases {
		if got := isNonTestSourceFile(tc.name); got != tc.want {
			t.Errorf("isNonTestSourceFile(%q): got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestCountConsumers_EmptyDirReturnsEmptyMap(t *testing.T) {
	tmp := t.TempDir()
	c, err := CountConsumers(tmp, []string{"recall"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(c["recall"]) != 0 {
		t.Fatalf("empty dir: expected 0 consumers, got %v", c["recall"])
	}
}
