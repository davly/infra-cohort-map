package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davly/infra-cohort-map/internal/scanner"
)

// update regenerates the golden files: `go test ./cmd/... -update`.
var update = flag.Bool("update", false, "update golden output files in testdata/")

// normEOL strips CR so golden comparison is line-ending agnostic (this
// box checks out .go/testdata with CRLF; generated output is always LF).
func normEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// checkGolden compares got against testdata/<name>, modulo line endings.
// With -update it (re)writes the golden file instead.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run `go test ./cmd/... -update` to create): %v", name, err)
	}
	if !bytes.Equal(normEOL(got), normEOL(want)) {
		t.Fatalf("golden %s mismatch (run `go test ./cmd/... -update` to refresh).\n--- got (%d bytes) ---\n%s", name, len(got), got)
	}
}

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

	// go.mod edges make the census-vs-module divergence visible in the golden:
	// academy REQUIRES recall's module (a real dependency); arbiter only hosts
	// a recall pattern dir (census hit) but its go.mod REQUIRES + local-REPLACES
	// aicore instead. So recall is a census-consumer of {academy,arbiter} but a
	// module-consumer of {academy}; aicore is a census-consumer of none but a
	// module-consumer of {arbiter} — the receipt inverts the proxy.
	mustWrite(t, filepath.Join(flagships, "academy"), "go.mod",
		"module example.com/academy\n\ngo 1.22\n\nrequire example.com/recall v1.0.0\n")
	mustWrite(t, filepath.Join(flagships, "arbiter"), "go.mod",
		"module example.com/arbiter\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n\nreplace example.com/aicore => ../../foundation/aicore\n")

	common = []string{
		"--infra-dir=" + infra,
		"--engines-dir=" + engines,
		"--foundation-dir=" + foundation,
		"--flagships-dir=" + flagships,
		"--checkout-root=" + root,
	}
	return root, common
}

// --- Wave-3 #icm-3: yamlStr control-char hardening ---------------------
//
// toYAML emits each scalar on a single physical line ("    name: " +
// yamlStr(v) + "\n"). yamlStr must therefore never let a raw control
// character (newline / CR / tab) survive into the quoted value: a raw
// newline in a name/module/note would spill onto the next line and
// corrupt the document structure. The escaped output must also remain
// parseable and round-trip to the original. YAML double-quoted scalars
// use JSON-compatible escapes (\n \r \t \" \\), so encoding/json is a
// faithful, dependency-free parser for exactly this subset.
func TestYamlStr_EscapesControlCharsAndRoundTrips(t *testing.T) {
	cases := []string{
		"plain",
		"with\nnewline",
		"with\rcarriage-return",
		"with\ttab",
		"quote\"inside",
		`back\slash`,
		"mixed\n\r\t\"\\end",
		"trailing-newline\n",
	}
	for _, in := range cases {
		got := yamlStr(in)

		// 1. No raw control char may survive into a single-line scalar.
		if strings.ContainsAny(got, "\n\r\t") {
			t.Errorf("yamlStr(%q) leaked a raw control char into output: %q", in, got)
		}

		// 2. Output must stay on exactly one line.
		if n := strings.Count(got, "\n"); n != 0 {
			t.Errorf("yamlStr(%q) produced %d embedded newlines, want 0: %q", in, n, got)
		}

		// 3. The quoted scalar must be parseable and round-trip back to
		//    the original (encoding/json parses the YAML double-quoted
		//    escape subset faithfully).
		var decoded string
		if err := json.Unmarshal([]byte(got), &decoded); err != nil {
			t.Errorf("yamlStr(%q) = %s is not parseable: %v", in, got, err)
			continue
		}
		if decoded != in {
			t.Errorf("round-trip mismatch: yamlStr(%q) decoded back to %q", in, decoded)
		}
	}
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

// --- Wave-2 golden-output tests (#24) ---------------------------------
//
// These run the full CLI pipeline (scan + render) over the deterministic
// scaffoldTree and compare to checked-in golden files. The output is
// machine-independent because --checkout-root makes paths relative and
// --no-scanned-at drops the timestamp. Each test also asserts the output
// is byte-identical across runs so a future non-determinism regression
// fails even before the golden diverges.

func TestGolden_ScanYAML(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{"scan", "--format=yaml", "--no-scanned-at"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("scan yaml: exit %d stderr=%q", code, errb.String())
	}
	checkGolden(t, "scan.yaml", out.Bytes())

	var out2 bytes.Buffer
	run(args, &out2, &errb)
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Fatal("scan YAML output not byte-identical across runs")
	}
}

func TestGolden_ScanJSON(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{"scan", "--format=json", "--no-scanned-at"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("scan json: exit %d stderr=%q", code, errb.String())
	}
	checkGolden(t, "scan.json", out.Bytes())

	var out2 bytes.Buffer
	run(args, &out2, &errb)
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Fatal("scan JSON output not byte-identical across runs")
	}
}

func TestGolden_RenderSVG(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{
		"render", "--out=-", "--format=svg",
		"--title=Golden Map", "--subtitle=deterministic fixture",
		"--snapshot-date=2026-01-01", "--width=1200", "--height=800",
		"--no-scanned-at",
	}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("render svg: exit %d stderr=%q", code, errb.String())
	}
	checkGolden(t, "render.svg", out.Bytes())

	var out2 bytes.Buffer
	run(args, &out2, &errb)
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Fatal("render SVG output not byte-identical across runs")
	}
}

// --- module_consumers (go.mod receipt-grade dependency edge) ----------

// snapDoc is a partial view of the JSON snapshot exposing both the census
// (consumer_*) and the receipt (module_consumer_*) fields per component.
type snapDoc struct {
	Components []struct {
		Name                string   `json:"name"`
		Layer               string   `json:"layer"`
		GoModule            string   `json:"go_module"`
		ConsumerCount       int      `json:"consumer_count"`
		Consumers           []string `json:"consumers"`
		ModuleConsumerCount *int     `json:"module_consumer_count"`
		ModuleConsumers     []string `json:"module_consumers"`
	} `json:"components"`
}

func parseSnap(t *testing.T, b []byte) snapDoc {
	t.Helper()
	var d snapDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v\n%s", err, b)
	}
	return d
}

// TestRun_ModuleConsumersDivergeFromCensus asserts the two signals are emitted
// side by side and diverge exactly as the fidelity audit describes: the
// scaffold's academy REQUIRES recall (module edge) while arbiter only hosts a
// recall pattern dir (census edge) and instead REQUIRES+REPLACEs aicore. So
// recall's census (2) exceeds its module degree (1), and aicore's module
// degree (1) exceeds its census (0) — the receipt inverts the proxy.
func TestRun_ModuleConsumersDivergeFromCensus(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"scan", "--format=json", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("scan json: exit %d stderr=%q", code, errb.String())
	}
	d := parseSnap(t, out.Bytes())

	var sawRecall, sawAicore bool
	for _, c := range d.Components {
		// Every Go-module component must carry the count (present, may be 0);
		// non-Go components omit it. All four scaffold components are Go.
		if c.GoModule != "" && c.ModuleConsumerCount == nil {
			t.Errorf("%s has go_module %q but no module_consumer_count", c.Name, c.GoModule)
		}
		switch c.Name {
		case "recall":
			sawRecall = true
			if c.ConsumerCount != 2 {
				t.Errorf("recall census consumer_count: got %d want 2", c.ConsumerCount)
			}
			if c.ModuleConsumerCount == nil || *c.ModuleConsumerCount != 1 {
				t.Errorf("recall module_consumer_count: got %v want 1", c.ModuleConsumerCount)
			}
			if len(c.ModuleConsumers) != 1 || c.ModuleConsumers[0] != "academy" {
				t.Errorf("recall module_consumers: got %v want [academy]", c.ModuleConsumers)
			}
			if !(c.ConsumerCount > *c.ModuleConsumerCount) {
				t.Errorf("recall: expected census (%d) > module (%d)", c.ConsumerCount, *c.ModuleConsumerCount)
			}
		case "aicore":
			sawAicore = true
			if c.ConsumerCount != 0 {
				t.Errorf("aicore census consumer_count: got %d want 0", c.ConsumerCount)
			}
			if c.ModuleConsumerCount == nil || *c.ModuleConsumerCount != 1 {
				t.Errorf("aicore module_consumer_count: got %v want 1", c.ModuleConsumerCount)
			}
			if len(c.ModuleConsumers) != 1 || c.ModuleConsumers[0] != "arbiter" {
				t.Errorf("aicore module_consumers: got %v want [arbiter]", c.ModuleConsumers)
			}
			if !(*c.ModuleConsumerCount > c.ConsumerCount) {
				t.Errorf("aicore: expected module (%d) > census (%d)", *c.ModuleConsumerCount, c.ConsumerCount)
			}
		}
	}
	if !sawRecall || !sawAicore {
		t.Fatalf("missing components: recall=%v aicore=%v", sawRecall, sawAicore)
	}
}

// TestRun_RealEstateModuleDegree is the land gate against the live checkout.
// It skips when the estate is absent (mirrors the real-tree render smoke).
//
// It encodes the flow-consumer-graph.md finding as executable invariants
// rather than a brittle exact count (the estate gains consumers over time and
// hosts worktree copies, so an `== N` would rot):
//   - aicore: module degree >= 33 (the audit's floor) while its pattern census
//     is far smaller — the receipt reveals the hub the proxy hides;
//   - lore: module degree EXACTLY 1 (nexus) while its census is large — the
//     proxy inflates a single real consumer into dozens;
//   - the two signals diverge in OPPOSITE directions.
//
// The audit stated 33 for aicore; today's honest parse is higher (extra infra
// the audit miscounted + a newer flagship consumer). The floor keeps the gate
// true without pinning a number that legitimately grows.
func TestRun_RealEstateModuleDegree(t *testing.T) {
	if _, err := os.Stat("C:/limitless/foundation/aicore"); err != nil {
		t.Skip("limitless estate not present")
	}
	var out, errb bytes.Buffer
	if code := run([]string{"scan", "--format=json", "--no-scanned-at"}, &out, &errb); code != exitOK {
		t.Fatalf("real-estate scan: exit %d stderr=%q", code, errb.String())
	}
	d := parseSnap(t, out.Bytes())

	byName := map[string]struct {
		census int
		module int
		mods   []string
	}{}
	for _, c := range d.Components {
		m := 0
		if c.ModuleConsumerCount != nil {
			m = *c.ModuleConsumerCount
		}
		byName[c.Name] = struct {
			census int
			module int
			mods   []string
		}{c.ConsumerCount, m, c.ModuleConsumers}
	}

	aic, ok := byName["aicore"]
	if !ok {
		t.Fatal("aicore not in snapshot")
	}
	const aicoreFloor = 33 // audit-measured minimum; grows as the estate grows
	if aic.module < aicoreFloor {
		t.Errorf("aicore module degree: got %d want >= %d; module_consumers=%v", aic.module, aicoreFloor, aic.mods)
	}
	if !(aic.module > aic.census) {
		t.Errorf("aicore inversion broken: module=%d census=%d (module must dwarf the pattern census)", aic.module, aic.census)
	}
	// The parse must genuinely span engines + infrastructure, not just one root.
	for _, want := range []string{"causal", "echo", "oracle", "parallax", "synthesis", "conduit", "nexus", "recall", "vault"} {
		if !contains(aic.mods, want) {
			t.Errorf("aicore module_consumers missing %q; got %v", want, aic.mods)
		}
	}

	lore, ok := byName["lore"]
	if !ok {
		t.Fatal("lore not in snapshot")
	}
	if lore.module != 1 || len(lore.mods) != 1 || lore.mods[0] != "nexus" {
		t.Errorf("lore module degree: got count=%d consumers=%v want 1 [nexus]", lore.module, lore.mods)
	}
	if !(lore.census > lore.module) {
		t.Errorf("lore inversion broken: census=%d module=%d (pattern census must dwarf the single real consumer)", lore.census, lore.module)
	}

	t.Logf("land-gate receipt: aicore module=%d census=%d | lore module=%d census=%d", aic.module, aic.census, lore.module, lore.census)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
