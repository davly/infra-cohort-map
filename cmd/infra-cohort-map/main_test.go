package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davly/infra-cohort-map/internal/scanner"
)

// mustWrite creates dir and writes name with body, failing the test on error.
func mustWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scaffoldTree builds a small, fully-deterministic limitless layout under
// a temp dir and returns the checkout root plus the common flag args that
// point the CLI at it. The shape: recall (5-of-5, load-bearing, KAT-1
// pinned) + codex (placeholder) infra, one engine (causal), foundation
// aicore, and two flagships consuming recall via internal/.
func scaffoldTree(t *testing.T) (root string, common []string) {
	t.Helper()
	root = t.TempDir()
	infra := filepath.Join(root, "infrastructure")
	engines := filepath.Join(root, "engines")
	foundation := filepath.Join(root, "foundation", "aicore")
	flagships := filepath.Join(root, "flagships")

	mustWrite(t, filepath.Join(infra, "recall"), "go.mod", "module example.com/recall\n\ngo 1.22\n")
	for _, p := range scanner.CohortPackages {
		mustWrite(t, filepath.Join(infra, "recall", "internal", p), p+".go", "package "+p+"\n")
	}
	mustWrite(t, filepath.Join(infra, "recall", "internal", "svc"), "svc.go",
		"package svc\nimport \"example.com/recall/internal/mirrormark\"\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	mustWrite(t, filepath.Join(infra, "recall"), "kat1.go",
		"package recall\nconst KAT1 = \""+scanner.KAT1HexCanonical+"\"\n")

	mustWrite(t, filepath.Join(infra, "codex"), "go.mod", "module example.com/codex\n\ngo 1.22\n")

	mustWrite(t, filepath.Join(engines, "causal"), "go.mod", "module example.com/causal\n\ngo 1.22\n")

	mustWrite(t, foundation, "go.mod", "module example.com/aicore\n\ngo 1.22\n")

	mustWrite(t, filepath.Join(flagships, "academy", "internal", "recall"), "x.go", "package recall\n")
	mustWrite(t, filepath.Join(flagships, "arbiter", "internal", "recall"), "x.go", "package recall\n")

	common = []string{
		"--infra-dir=" + infra,
		"--engines-dir=" + engines,
		"--foundation-dir=" + foundation,
		"--flagships-dir=" + flagships,
		"--checkout-root=" + root,
	}
	return root, common
}

func TestRun_RenderRejectsBadDimensions(t *testing.T) {
	_, common := scaffoldTree(t)
	for _, dim := range []string{"--width=0", "--height=0", "--width=-1", "--height=-5", "--width=200000", "--height=200000"} {
		var out, errb bytes.Buffer
		args := append([]string{"render", "--out=-", dim}, common...)
		code := run(args, &out, &errb)
		if code != exitUsage {
			t.Errorf("%s: got exit %d want %d (usage); stderr=%q", dim, code, exitUsage, errb.String())
		}
	}
}

func TestRun_RenderAcceptsValidDimensions(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	args := append([]string{"render", "--out=-", "--width=1200", "--height=800", "--snapshot-date=2026-01-01"}, common...)
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("valid dims: got exit %d stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "</svg>") {
		t.Fatal("valid render did not produce a closed SVG")
	}
}

func TestRun_ScanSuppressesScannedAt(t *testing.T) {
	_, common := scaffoldTree(t)

	var out, errb bytes.Buffer
	if code := run(append([]string{"scan", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("scan: exit %d stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "\ngenerated_at:\n") {
		t.Errorf("expected suppressed (empty) generated_at, got:\n%s", out.String())
	}

	var out2, errb2 bytes.Buffer
	if code := run(append([]string{"scan"}, common...), &out2, &errb2); code != exitOK {
		t.Fatalf("scan (with stamp): exit %d stderr=%q", code, errb2.String())
	}
	if strings.Contains(out2.String(), "\ngenerated_at:\n") {
		t.Error("expected a timestamp when --no-scanned-at not set, got empty generated_at")
	}
	if !strings.Contains(out2.String(), "generated_at: 20") {
		t.Error("expected RFC3339 timestamp value starting with 20xx")
	}
}

func TestRun_ScanEmitsCheckoutRelativePaths(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"scan", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("scan: exit %d stderr=%q", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, `path: "infrastructure/recall"`) {
		t.Errorf("expected checkout-relative path infrastructure/recall, got:\n%s", s)
	}
	if !strings.Contains(s, `path: "engines/causal"`) {
		t.Errorf("expected checkout-relative path engines/causal, got:\n%s", s)
	}
	// No absolute drive-letter path should leak into the snapshot.
	if strings.Contains(s, `path: "C:/`) || strings.Contains(s, `:\`) {
		t.Errorf("absolute path leaked into output:\n%s", s)
	}
}

func TestRun_ScanJSONValidDeterministicAndRelative(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"scan", "--format=json", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("scan json: exit %d stderr=%q", code, errb.String())
	}

	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Components    []struct {
			Name          string          `json:"name"`
			Path          string          `json:"path"`
			PackageStatus map[string]bool `json:"package_status"`
			ConsumerCount int             `json:"consumer_count"`
			Consumers     []string        `json:"consumers"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if doc.SchemaVersion != 1 {
		t.Errorf("schema_version: got %d want 1", doc.SchemaVersion)
	}
	if len(doc.Components) != 4 {
		t.Fatalf("components: got %d want 4", len(doc.Components))
	}
	var sawRecall bool
	for _, c := range doc.Components {
		if c.Name == "recall" {
			sawRecall = true
			if c.ConsumerCount != 2 {
				t.Errorf("recall consumer_count: got %d want 2", c.ConsumerCount)
			}
			if c.Path != "infrastructure/recall" {
				t.Errorf("recall path: got %q want infrastructure/recall", c.Path)
			}
			if len(c.PackageStatus) != 5 {
				t.Errorf("recall package_status: got %d keys want 5", len(c.PackageStatus))
			}
		}
	}
	if !sawRecall {
		t.Error("recall missing from JSON components")
	}

	// Deterministic: byte-identical across runs.
	var out2 bytes.Buffer
	if code := run(append([]string{"scan", "--format=json", "--no-scanned-at"}, common...), &out2, &errb); code != exitOK {
		t.Fatalf("scan json (2nd): exit %d", code)
	}
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Error("JSON output not byte-identical across runs")
	}
}

func TestRun_RenderJSONFormat(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"render", "--out=-", "--format=json", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("render json: exit %d stderr=%q", code, errb.String())
	}
	var probe map[string]any
	if err := json.Unmarshal(out.Bytes(), &probe); err != nil {
		t.Fatalf("render --format=json did not emit valid JSON: %v", err)
	}
}
