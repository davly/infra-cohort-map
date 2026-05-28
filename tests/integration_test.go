// Package tests holds cross-package integration smoke tests for the
// infra-cohort-map binary. Unit tests live next to their packages.
package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davly/infra-cohort-map/internal/relate"
	"github.com/davly/infra-cohort-map/internal/render"
	"github.com/davly/infra-cohort-map/internal/scanner"
)

// writeFile creates dir and writes name with body.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEndToEnd_ScaffoldedTreeScannedAndRendered builds a small in-memory
// limitless layout, runs the full pipeline, and asserts the output SVG
// contains the expected component names.
func TestEndToEnd_ScaffoldedTreeScannedAndRendered(t *testing.T) {
	tmp := t.TempDir()
	infraDir := filepath.Join(tmp, "infrastructure")
	enginesDir := filepath.Join(tmp, "engines")
	foundationDir := filepath.Join(tmp, "foundation", "aicore")
	flagshipsDir := filepath.Join(tmp, "flagships")

	// Two infra: recall (5-of-5) + codex (placeholders only).
	writeFile(t, filepath.Join(infraDir, "recall"), "go.mod", "module example.com/recall\n\ngo 1.22\n")
	for _, p := range scanner.CohortPackages {
		writeFile(t, filepath.Join(infraDir, "recall", "internal", p), p+".go", "package "+p+"\n")
	}
	writeFile(t, filepath.Join(infraDir, "recall", "internal", "svc"), "svc.go",
		"package svc\nimport \"example.com/recall/internal/mirrormark\"\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	writeFile(t, filepath.Join(infraDir, "recall"), "kat1.go",
		"package recall\nconst KAT1 = \""+scanner.KAT1HexCanonical+"\"\n")

	writeFile(t, filepath.Join(infraDir, "codex"), "go.mod", "module example.com/codex\n\ngo 1.22\n")
	for _, p := range scanner.CohortPackages {
		if err := os.MkdirAll(filepath.Join(infraDir, "codex", "internal", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// One engine.
	writeFile(t, filepath.Join(enginesDir, "causal"), "go.mod", "module example.com/causal\n\ngo 1.22\n")

	// foundation/aicore (single-root go.mod).
	writeFile(t, foundationDir, "go.mod", "module example.com/aicore\n\ngo 1.22\n")

	// Two flagships consuming recall.
	writeFile(t, filepath.Join(flagshipsDir, "academy", "internal", "recall"), "x.go", "package recall\n")
	writeFile(t, filepath.Join(flagshipsDir, "arbiter", "internal", "recall"), "x.go", "package recall\n")

	snap, err := scanner.ScanAll(scanner.Roots{
		InfrastructureDir: infraDir,
		EnginesDir:        enginesDir,
		FoundationDir:     foundationDir,
	})
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(snap.Components) != 4 {
		t.Fatalf("components: got %d want 4", len(snap.Components))
	}

	consumers, err := relate.CountConsumers(flagshipsDir, []string{"recall", "codex", "causal", "aicore"})
	if err != nil {
		t.Fatalf("CountConsumers: %v", err)
	}
	if len(consumers["recall"]) != 2 {
		t.Fatalf("recall consumers: got %v want 2", consumers["recall"])
	}

	out := render.Render(snap, consumers, render.Options{
		Title:    "Test Map",
		Snapshot: "2026-05-28",
	})
	if !bytes.Contains(out, []byte(">recall<")) {
		t.Error("recall not rendered")
	}
	if !bytes.Contains(out, []byte(">codex<")) {
		t.Error("codex not rendered")
	}
	if !bytes.Contains(out, []byte(">causal<")) {
		t.Error("causal not rendered")
	}
	if !bytes.Contains(out, []byte(">aicore<")) {
		t.Error("aicore not rendered")
	}
	// recall must have the load-bearing thick stroke + KAT-1 halo.
	if !bytes.Contains(out, []byte(`stroke-width="4"`)) {
		t.Error("recall: load-bearing stroke missing")
	}
	if !bytes.Contains(out, []byte(`stroke="#F59E0B"`)) {
		t.Error("recall: KAT-1 halo missing")
	}
}

// TestRender_ProducesNonTrivialSizeFromRealTree is a smoke test that
// runs the full pipeline against the actual limitless checkout (if
// present) and confirms the SVG is non-trivial.
func TestRender_ProducesNonTrivialSizeFromRealTree(t *testing.T) {
	if _, err := os.Stat("C:/limitless/infrastructure"); err != nil {
		t.Skip("limitless infra tree not present")
	}
	snap, err := scanner.ScanAll(scanner.DefaultRoots())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	c, err := relate.CountConsumers(relate.DefaultFlagshipsDir(), nil)
	if err != nil {
		t.Skipf("flagships dir not readable: %v", err)
	}
	out := render.Render(snap, c, render.Options{})
	if len(out) < 5000 {
		t.Fatalf("rendered SVG suspiciously small: %d bytes", len(out))
	}
	if !strings.Contains(string(out), "</svg>") {
		t.Fatal("real-tree SVG not closed")
	}
}
