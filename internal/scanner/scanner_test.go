package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helpers ---------------------------------------------------------------

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s/%s: %v", dir, name, err)
	}
}

// scaffoldGoInfra creates an infra root with: go.mod, the named subset
// of cohort packages (each with a non-test marker.go), optional
// load-bearing marker.go elsewhere, optional KAT-1 pin file.
func scaffoldGoInfra(t *testing.T, name string, pkgs []string, loadBearing, kat1Pinned bool) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	writeFile(t, root, "go.mod", "module example.com/"+name+"\n\ngo 1.22\n")
	for _, pkg := range pkgs {
		writeFile(t, filepath.Join(root, "internal", pkg), "marker.go",
			"package "+pkg+"\n\nfunc Marker() {}\n")
	}
	if loadBearing {
		writeFile(t, filepath.Join(root, "internal", "service"), "service.go",
			"package service\n\nimport \"example.com/"+name+"/internal/mirrormark\"\n\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	}
	if kat1Pinned {
		writeFile(t, root, "kat1.go",
			"package "+name+"\n\nconst KAT1Hex = \""+KAT1HexCanonical+"\"\n")
	}
	return root
}

// TESTS -----------------------------------------------------------------

func TestDetectSubstrate_Go(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	writeFile(t, root, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	sub, mod := detectSubstrate(root)
	if sub != "go" {
		t.Fatalf("substrate: got %q want go", sub)
	}
	if mod != "example.com/foo" {
		t.Fatalf("module path: got %q want example.com/foo", mod)
	}
}

func TestDetectSubstrate_Python(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"foo\"\n")
	sub, _ := detectSubstrate(root)
	if sub != "python" {
		t.Fatalf("substrate: got %q want python", sub)
	}
}

func TestDetectSubstrate_Rust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	writeFile(t, root, "Cargo.toml", "[package]\nname = \"foo\"\n")
	sub, _ := detectSubstrate(root)
	if sub != "rust" {
		t.Fatalf("substrate: got %q want rust", sub)
	}
}

func TestDetectSubstrate_Crystal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	writeFile(t, root, "shard.yml", "name: foo\nversion: 0.1.0\n")
	sub, _ := detectSubstrate(root)
	if sub != "crystal" {
		t.Fatalf("substrate: got %q want crystal", sub)
	}
}

func TestDetectSubstrate_TypeScript(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	writeFile(t, root, "package.json", `{"name":"foo"}`)
	writeFile(t, root, "tsconfig.json", `{}`)
	sub, _ := detectSubstrate(root)
	if sub != "typescript" {
		t.Fatalf("substrate: got %q want typescript", sub)
	}
}

func TestDetectSubstrate_Unknown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "infra")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sub, _ := detectSubstrate(root)
	if sub != "unknown" {
		t.Fatalf("substrate: got %q want unknown", sub)
	}
}

func TestDetectSubstrate_NestedGoMod(t *testing.T) {
	// Mirrors the nexus layout (src/api/go.mod).
	root := filepath.Join(t.TempDir(), "nexus")
	writeFile(t, filepath.Join(root, "src", "api"), "go.mod", "module example.com/nexus\n\ngo 1.22\n")
	sub, mod := detectSubstrate(root)
	if sub != "go" {
		t.Fatalf("substrate: got %q want go (nested)", sub)
	}
	if mod != "example.com/nexus" {
		t.Fatalf("nested module: got %q", mod)
	}
}

func TestDetectCohortPackages_FiveOfFive(t *testing.T) {
	root := scaffoldGoInfra(t, "full", CohortPackages[:], false, false)
	status, n := detectCohortPackages(root)
	if n != 5 {
		t.Fatalf("cohort count: got %d want 5", n)
	}
	for i, ok := range status {
		if !ok {
			t.Errorf("pkg %d (%s) missing", i, CohortPackages[i])
		}
	}
}

func TestDetectCohortPackages_PlaceholderDirsExcluded(t *testing.T) {
	// codex pattern: dirs exist but contain no .go files → should
	// NOT count as "present".
	root := filepath.Join(t.TempDir(), "codex-like")
	writeFile(t, root, "go.mod", "module example.com/codex\n\ngo 1.22\n")
	for _, p := range CohortPackages {
		if err := os.MkdirAll(filepath.Join(root, "internal", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, n := detectCohortPackages(root)
	if n != 0 {
		t.Fatalf("empty placeholders: got %d want 0", n)
	}
}

func TestDetectCohortPackages_TestOnlyExcluded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "test-only")
	writeFile(t, root, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	for _, p := range CohortPackages {
		writeFile(t, filepath.Join(root, "internal", p), "x_test.go",
			"package "+p+"\n")
	}
	_, n := detectCohortPackages(root)
	if n != 0 {
		t.Fatalf("test-only files: got %d want 0", n)
	}
}

func TestDetectCohortPackages_PkgLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pkg-layout")
	writeFile(t, root, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	// Use pkg/<name>/ instead of internal/<name>/ — recall pattern.
	for _, p := range CohortPackages {
		writeFile(t, filepath.Join(root, "pkg", p), p+".go", "package "+p+"\n")
	}
	_, n := detectCohortPackages(root)
	if n != 5 {
		t.Fatalf("pkg layout: got %d want 5", n)
	}
}

func TestDetectLoadBearing_ProductionSign(t *testing.T) {
	root := scaffoldGoInfra(t, "lb", CohortPackages[:], true, false)
	if !detectLoadBearing(root) {
		t.Fatal("load-bearing: expected true (production code calls Sign)")
	}
}

func TestDetectLoadBearing_TestOnlyIsFalse(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tonly")
	writeFile(t, root, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	writeFile(t, root, "x_test.go", "package foo\n\nfunc TestX() { mirrormark.Sign() }\n")
	if detectLoadBearing(root) {
		t.Fatal("load-bearing: test-only Sign call must be reported false")
	}
}

func TestDetectLoadBearing_OversizeFileSkipped(t *testing.T) {
	// A .go file larger than the 1MB read cap is not scanned for the
	// Sign needle (matches detectKAT1Pin's guard) — so a Sign call that
	// lives only inside such a giant file is reported false. The point
	// is the cap is active and the read is bounded.
	root := filepath.Join(t.TempDir(), "huge")
	pad := strings.Repeat("// padding to push past the 1MB read cap\n", 30000)
	if len(pad) <= 1<<20 {
		t.Fatalf("test padding too small: %d bytes, need > %d", len(pad), 1<<20)
	}
	writeFile(t, filepath.Join(root, "internal", "svc"), "big.go",
		"package svc\n"+pad+"\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	if detectLoadBearing(root) {
		t.Fatal("load-bearing: Sign call inside an oversize file must be skipped (read cap)")
	}

	// A small file with the same call IS detected — cap does not block
	// ordinary source.
	writeFile(t, filepath.Join(root, "internal", "small"), "small.go",
		"package small\n\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	if !detectLoadBearing(root) {
		t.Fatal("load-bearing: small Sign call-site must still be detected")
	}
}

func TestDetectKAT1Pin_Present(t *testing.T) {
	root := scaffoldGoInfra(t, "kat", nil, false, true)
	if !detectKAT1Pin(root) {
		t.Fatal("KAT-1 pin: expected true")
	}
}

func TestDetectKAT1Pin_Absent(t *testing.T) {
	root := scaffoldGoInfra(t, "nokat", nil, false, false)
	if detectKAT1Pin(root) {
		t.Fatal("KAT-1 pin: expected false")
	}
}

func TestDetectInternalDeps_LooksUpKnownNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "downstream")
	writeFile(t, root, "go.mod", "module example.com/foo\n\ngo 1.22\n")
	// Create internal/recall and internal/codex — both in knownInfraNames().
	for _, n := range []string{"recall", "codex", "totally-unknown"} {
		writeFile(t, filepath.Join(root, "internal", n), "x.go", "package "+n+"\n")
	}
	deps := detectInternalDeps(root)
	if len(deps) != 2 {
		t.Fatalf("deps: got %v want [codex recall]", deps)
	}
	if deps[0] != "codex" || deps[1] != "recall" {
		t.Fatalf("deps order: got %v want [codex recall]", deps)
	}
}

func TestScanAll_SortsByLayerThenName(t *testing.T) {
	tmp := t.TempDir()
	infra := filepath.Join(tmp, "infrastructure")
	engines := filepath.Join(tmp, "engines")
	foundation := filepath.Join(tmp, "foundation", "aicore")

	// Create two infra components.
	for _, n := range []string{"zeta-svc", "alpha-svc"} {
		writeFile(t, filepath.Join(infra, n), "go.mod", "module example.com/"+n+"\n\ngo 1.22\n")
	}
	// Create one engine.
	writeFile(t, filepath.Join(engines, "myengine"), "go.mod", "module example.com/myengine\n\ngo 1.22\n")
	// Create foundation.
	writeFile(t, foundation, "go.mod", "module example.com/aicore\n\ngo 1.22\n")

	roots := Roots{InfrastructureDir: infra, EnginesDir: engines, FoundationDir: foundation}
	snap, err := ScanAll(roots)
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(snap.Components) != 4 {
		t.Fatalf("components: got %d want 4", len(snap.Components))
	}
	// Sort order: engine < foundation < infrastructure.
	wantOrder := []string{"myengine", "aicore", "alpha-svc", "zeta-svc"}
	for i, c := range snap.Components {
		if c.Name != wantOrder[i] {
			t.Errorf("idx %d: got %q want %q", i, c.Name, wantOrder[i])
		}
	}
}

func TestScanAll_RealLimitlessLayoutSubset(t *testing.T) {
	// Smoke-test against the real tree if available. Skip if we're
	// running on a CI box without the limitless checkout.
	infra := filepath.FromSlash("C:/limitless/infrastructure")
	if _, err := os.Stat(infra); err != nil {
		t.Skip("C:/limitless/infrastructure not present (CI)")
	}
	snap, err := ScanAll(DefaultRoots())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(snap.Components) < 5 {
		t.Fatalf("expected at least 5 components from real tree, got %d", len(snap.Components))
	}
	// Recall is the canonical full 5-of-5 reference — it must show up.
	var foundRecall bool
	for _, c := range snap.Components {
		if c.Name == "recall" {
			foundRecall = true
			break
		}
	}
	if !foundRecall {
		t.Error("recall not found in scan output")
	}
}

func TestComponent_NotesAttachedForFiveOfFiveNotLoadBearing(t *testing.T) {
	root := scaffoldGoInfra(t, "shape", CohortPackages[:], false, false)
	c := scanOne("shape", root, LayerInfrastructure)
	if c.CohortCount != 5 {
		t.Fatalf("cohort count: got %d want 5", c.CohortCount)
	}
	if c.LoadBearing {
		t.Fatal("load-bearing must be false")
	}
	var sawNote bool
	for _, n := range c.Notes {
		if n == "5-of-5 present but not load-bearing" {
			sawNote = true
			break
		}
	}
	if !sawNote {
		t.Fatalf("expected note attached, got %v", c.Notes)
	}
}

func TestComponent_NotesAttachedForZeroCohort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bare")
	writeFile(t, root, "go.mod", "module example.com/bare\n\ngo 1.22\n")
	c := scanOne("bare", root, LayerInfrastructure)
	if c.CohortCount != 0 {
		t.Fatalf("cohort count: got %d want 0", c.CohortCount)
	}
	var sawNote bool
	for _, n := range c.Notes {
		if n == "no R174 cohort packages present" {
			sawNote = true
			break
		}
	}
	if !sawNote {
		t.Fatalf("expected zero-cohort note, got %v", c.Notes)
	}
}

func TestKAT1HexCanonical_LengthAndHex(t *testing.T) {
	if len(KAT1HexCanonical) != 64 {
		t.Fatalf("KAT-1 hex length: got %d want 64", len(KAT1HexCanonical))
	}
	for _, c := range KAT1HexCanonical {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			t.Fatalf("KAT-1 hex contains non-hex char: %c", c)
		}
	}
}

func TestCohortPackages_FiveNames(t *testing.T) {
	if len(CohortPackages) != 5 {
		t.Fatalf("cohort packages: got %d want 5", len(CohortPackages))
	}
	want := []string{"mirrormark", "honest", "legal", "manifest", "firewall"}
	for i, n := range want {
		if CohortPackages[i] != n {
			t.Errorf("idx %d: got %q want %q", i, CohortPackages[i], n)
		}
	}
}
