package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/davly/infra-cohort-map/internal/scanner"
)

// --- Wave-2 cmd-layer tests (#icm-1) ----------------------------------
//
// These close the biggest coverage hole in the cmd layer: dispatch
// (run/printUsage), the `list` sub-command (cmdList), the `--projection`
// flag (applyProjection — documented but previously untested), the
// file-writing branch of writeOut, and the escape/empty-root branches of
// relativizePath. All run over the deterministic scaffoldTree so output
// is machine-independent and byte-stable.

// TestGolden_List exercises cmdList end-to-end against a checked-in
// golden. The list output is the per-infra consumer count, sorted by
// descending count then name (relate.Consumers.Pairs). It also asserts
// byte-identical output across runs so a determinism regression fails
// before the golden diverges.
func TestGolden_List(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{"list"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("list: exit %d stderr=%q", code, errb.String())
	}
	checkGolden(t, "list.txt", out.Bytes())

	var out2 bytes.Buffer
	run(args, &out2, &errb)
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Fatal("list output not byte-identical across runs")
	}
}

// TestRun_ListContent asserts the exact list output independent of the
// golden file, so the consumer-count semantics (recall=2, others=0,
// sorted count-desc then name-asc) are pinned even if the golden is
// regenerated.
func TestRun_ListContent(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"list"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("list: exit %d stderr=%q", code, errb.String())
	}
	const want = "   2  recall\n   0  aicore\n   0  causal\n   0  codex\n"
	if got := out.String(); got != want {
		t.Errorf("list output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRun_ListMissingFlagshipsErrors covers cmdList's relate-error exit
// path: a flagships dir that does not exist makes CountConsumers fail and
// the command must return exitIO (not a silent zero-count clean).
func TestRun_ListMissingFlagshipsErrors(t *testing.T) {
	_, common := scaffoldTree(t)
	// Replace the flagships-dir flag with a non-existent path.
	args := []string{"list"}
	for _, a := range common {
		if strings.HasPrefix(a, "--flagships-dir=") {
			a = "--flagships-dir=" + filepath.Join(t.TempDir(), "nope")
		}
		args = append(args, a)
	}
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitIO {
		t.Fatalf("list missing flagships: got exit %d want %d (I/O); stderr=%q", code, exitIO, errb.String())
	}
}

// TestRun_RenderProjected exercises applyProjection: with
// --projection=projected every component is forced to a 5-of-5 cohort,
// load-bearing, KAT-1 pinned, with the projection note — while consumer
// counts (computed before projection) are preserved.
func TestRun_RenderProjected(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{"render", "--out=-", "--format=json", "--projection=projected", "--no-scanned-at"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("render projected: exit %d stderr=%q", code, errb.String())
	}

	var doc struct {
		Components []struct {
			Name          string          `json:"name"`
			CohortCount   int             `json:"cohort_count"`
			LoadBearing   bool            `json:"load_bearing"`
			KAT1Pinned    bool            `json:"kat1_pinned"`
			PackageStatus map[string]bool `json:"package_status"`
			ConsumerCount int             `json:"consumer_count"`
			Notes         []string        `json:"notes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("projected output not valid JSON: %v\n%s", err, out.String())
	}
	if len(doc.Components) != 4 {
		t.Fatalf("components: got %d want 4", len(doc.Components))
	}
	for _, c := range doc.Components {
		if c.CohortCount != 5 {
			t.Errorf("%s cohort_count: got %d want 5 (projected)", c.Name, c.CohortCount)
		}
		if !c.LoadBearing {
			t.Errorf("%s load_bearing: got false want true (projected)", c.Name)
		}
		if !c.KAT1Pinned {
			t.Errorf("%s kat1_pinned: got false want true (projected)", c.Name)
		}
		for pkg, present := range c.PackageStatus {
			if !present {
				t.Errorf("%s package_status[%s]: got false want true (projected)", c.Name, pkg)
			}
		}
		if len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "projected") {
			t.Errorf("%s notes: got %v want one projected note", c.Name, c.Notes)
		}
		// consumer counts are computed before projection, so recall must
		// still carry its 2 flagship consumers.
		if c.Name == "recall" && c.ConsumerCount != 2 {
			t.Errorf("recall consumer_count under projection: got %d want 2", c.ConsumerCount)
		}
	}

	// Projected output is deterministic across runs.
	var out2 bytes.Buffer
	run(args, &out2, &errb)
	if !bytes.Equal(out.Bytes(), out2.Bytes()) {
		t.Fatal("projected JSON not byte-identical across runs")
	}
}

// TestRun_WriteToFile covers writeOut's file branch (including the
// MkdirAll of a not-yet-existing parent dir) rather than the stdout
// branch every other test uses.
func TestRun_WriteToFile(t *testing.T) {
	_, common := scaffoldTree(t)
	// Nested path whose parent dirs do not exist yet -> exercises MkdirAll.
	outFile := filepath.Join(t.TempDir(), "sub", "dir", "snap.yaml")
	args := append([]string{"scan", "--format=yaml", "--no-scanned-at", "--out=" + outFile}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitOK {
		t.Fatalf("scan --out=file: exit %d stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "wrote ") || !strings.Contains(out.String(), "bytes)") {
		t.Errorf("expected a 'wrote <file> (<n> bytes)' confirmation, got: %q", out.String())
	}
	b, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}
	if !strings.Contains(string(b), "schema_version: 1") {
		t.Errorf("written file missing schema_version header:\n%s", b)
	}
	if !strings.Contains(string(b), `name: "recall"`) {
		t.Errorf("written file missing recall component:\n%s", b)
	}
}

// TestRelativizePath unit-tests the path-relativization helper's two
// fall-through branches (empty root, and a path that escapes root) which
// the golden pipeline never reaches — both must return the slash-form
// path unchanged rather than silently mangling it.
func TestRelativizePath(t *testing.T) {
	tmp := t.TempDir()
	inside := filepath.Join(tmp, "infrastructure", "recall")

	// Normal case: relative, forward-slashed.
	if got := relativizePath(tmp, inside); got != "infrastructure/recall" {
		t.Errorf("inside-root: got %q want infrastructure/recall", got)
	}

	// Empty root: returns ToSlash(p) unchanged.
	if got := relativizePath("", inside); got != filepath.ToSlash(inside) {
		t.Errorf("empty-root: got %q want %q", got, filepath.ToSlash(inside))
	}

	// Path equal to a parent of root -> Rel yields ".." -> abs returned.
	if got := relativizePath(inside, tmp); got != filepath.ToSlash(tmp) {
		t.Errorf("parent-of-root: got %q want %q (abs, not '..')", got, filepath.ToSlash(tmp))
	}

	// Sibling that escapes root -> Rel yields "../sibling" -> abs returned.
	a := filepath.Join(tmp, "a")
	b := filepath.Join(tmp, "b", "x")
	if got := relativizePath(a, b); got != filepath.ToSlash(b) {
		t.Errorf("escape-root: got %q want %q (abs, not '..\\b\\x')", got, filepath.ToSlash(b))
	}
}

// TestRun_Dispatch covers run()'s sub-command routing and both
// printUsage destinations (stdout for help, stderr for errors).
func TestRun_Dispatch(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring (empty = don't check)
		wantStderr string // substring (empty = don't check)
	}{
		{"no-args", nil, exitUsage, "", "Sub-commands:"},
		{"unknown", []string{"frobnicate"}, exitUsage, "", "unknown sub-command"},
		{"version-long", []string{"--version"}, exitOK, version, ""},
		{"version-short", []string{"-version"}, exitOK, version, ""},
		{"version-v", []string{"-v"}, exitOK, version, ""},
		{"help-word", []string{"help"}, exitOK, "Sub-commands:", ""},
		{"help-short", []string{"-h"}, exitOK, "Sub-commands:", ""},
		{"help-long", []string{"--help"}, exitOK, "Exit codes:", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := run(tc.args, &out, &errb)
			if code != tc.wantCode {
				t.Errorf("exit: got %d want %d (stderr=%q)", code, tc.wantCode, errb.String())
			}
			if tc.wantStdout != "" && !strings.Contains(out.String(), tc.wantStdout) {
				t.Errorf("stdout %q missing %q", out.String(), tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(errb.String(), tc.wantStderr) {
				t.Errorf("stderr %q missing %q", errb.String(), tc.wantStderr)
			}
		})
	}
}

// TestRun_VersionContract pins the version output to the machine-readable JSON
// contract (regulator automation parses it). Every spelling of the flag
// (-version / --version / -v) must emit the identical, byte-stable payload
// carrying the tool name, the binary version, and the snapshot schema_version
// — which must equal the schemaVersion constant actually stamped into snapshots.
func TestRun_VersionContract(t *testing.T) {
	for _, flagSpelling := range []string{"--version", "-version", "-v"} {
		t.Run(flagSpelling, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run([]string{flagSpelling}, &out, &errb); code != exitOK {
				t.Fatalf("%s: exit %d (stderr=%q)", flagSpelling, code, errb.String())
			}
			var got versionInfo
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("%s: output is not valid JSON: %v\noutput=%q", flagSpelling, err, out.String())
			}
			want := versionInfo{Tool: "infra-cohort-map", Version: version, SchemaVersion: schemaVersion}
			if got != want {
				t.Errorf("%s: got %+v want %+v", flagSpelling, got, want)
			}
			// Encoder emits exactly one line (compact JSON + trailing newline).
			if n := strings.Count(out.String(), "\n"); n != 1 {
				t.Errorf("%s: expected single-line JSON, got %d newlines in %q", flagSpelling, n, out.String())
			}
		})
	}
}

// TestVersion_SchemaMatchesSnapshot guards the single-source-of-truth wiring:
// the schema_version surfaced by -version must equal the schema_version
// actually stamped into an emitted snapshot, so the two cannot drift.
func TestVersion_SchemaMatchesSnapshot(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-version"}, &out, &errb); code != exitOK {
		t.Fatalf("-version: exit %d", code)
	}
	var vi versionInfo
	if err := json.Unmarshal(out.Bytes(), &vi); err != nil {
		t.Fatalf("-version: bad JSON: %v", err)
	}
	yaml := toYAML(scanner.Snapshot{}, nil, nil, "")
	if want := "schema_version: " + strconv.Itoa(vi.SchemaVersion); !strings.Contains(yaml, want) {
		t.Errorf("YAML snapshot missing %q (version reports schema_version=%d):\n%s", want, vi.SchemaVersion, yaml)
	}
}

// --- Wave-4 #icm-6: scan exit gates ----------------------------------
//
// scan --require-5of5 / --require-loadbearing turn the scan into a
// deterministic CI exit gate: exit 9 (exitGate) unless every scanned
// component clears the bar, exit 0 when all clear, and the snapshot is
// always emitted first so a failing run still leaves a report behind.

// scaffoldAllPass builds a layout whose only scanned component (recall) is
// 5-of-5, load-bearing, and KAT-1 pinned, with engines/foundation roots
// switched off — so every requested exit gate is satisfied. It returns the
// common flag args pointing the CLI at the tree.
func scaffoldAllPass(t *testing.T) (common []string) {
	t.Helper()
	root := t.TempDir()
	infra := filepath.Join(root, "infrastructure")
	flagships := filepath.Join(root, "flagships")

	mustWrite(t, filepath.Join(infra, "recall"), "go.mod", "module example.com/recall\n\ngo 1.22\n")
	for _, p := range scanner.CohortPackages {
		mustWrite(t, filepath.Join(infra, "recall", "internal", p), p+".go", "package "+p+"\n")
	}
	mustWrite(t, filepath.Join(infra, "recall", "internal", "svc"), "svc.go",
		"package svc\nimport \"example.com/recall/internal/mirrormark\"\nfunc Use() string { return mirrormark.Sign([32]byte{}, nil, nil) }\n")
	mustWrite(t, filepath.Join(infra, "recall"), "kat1.go",
		"package recall\nconst KAT1 = \""+scanner.KAT1HexCanonical+"\"\n")
	mustWrite(t, filepath.Join(flagships, "academy", "internal", "recall"), "x.go", "package recall\n")

	return []string{
		"--infra-dir=" + infra,
		"--engines-dir=",
		"--foundation-dir=",
		"--flagships-dir=" + flagships,
		"--checkout-root=" + root,
	}
}

// TestScanGate_FailExitCodes covers the failing gates against scaffoldTree,
// where only recall (1 of 4 components) clears either bar — so each gate
// must return exitGate and name exactly the three components below the bar.
func TestScanGate_FailExitCodes(t *testing.T) {
	cases := []struct {
		name       string
		flag       string
		wantReason string
	}{
		{"require-5of5", "--require-5of5", "cohort 0/5"},
		{"require-loadbearing", "--require-loadbearing", "not load-bearing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, common := scaffoldTree(t)
			args := append([]string{"scan", "--format=yaml", "--no-scanned-at", tc.flag}, common...)

			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != exitGate {
				t.Fatalf("%s: got exit %d want %d (gate); stderr=%q", tc.flag, code, exitGate, errb.String())
			}
			// The snapshot is emitted before the gate fails.
			if !strings.Contains(out.String(), `name: "recall"`) {
				t.Errorf("%s: snapshot not emitted on gate failure:\n%s", tc.flag, out.String())
			}
			// Diagnostic goes to stderr, names the three sub-bar components.
			es := errb.String()
			if !strings.Contains(es, "3 of 4 component(s) below the bar") {
				t.Errorf("%s: stderr missing failure tally:\n%s", tc.flag, es)
			}
			for _, name := range []string{"engine/causal", "foundation/aicore", "infrastructure/codex"} {
				if !strings.Contains(es, name+": "+tc.wantReason) {
					t.Errorf("%s: stderr missing %q reason for %s:\n%s", tc.flag, tc.wantReason, name, es)
				}
			}
			// recall clears the bar, so it must NOT be listed.
			if strings.Contains(es, "infrastructure/recall:") {
				t.Errorf("%s: recall clears the bar but was listed as failing:\n%s", tc.flag, es)
			}
		})
	}
}

// TestScanGate_BothFlagsListBothReasons verifies that requesting both gates
// reports both reasons, comma-joined, for a component below both bars.
func TestScanGate_BothFlagsListBothReasons(t *testing.T) {
	_, common := scaffoldTree(t)
	args := append([]string{"scan", "--no-scanned-at", "--require-5of5", "--require-loadbearing"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitGate {
		t.Fatalf("both gates: got exit %d want %d (gate); stderr=%q", code, exitGate, errb.String())
	}
	if !strings.Contains(errb.String(), "engine/causal: cohort 0/5, not load-bearing") {
		t.Errorf("both gates: expected comma-joined reasons for causal:\n%s", errb.String())
	}
}

// TestScanGate_PassExitCode covers the all-clear path: with a tree whose
// every scanned component is 5-of-5 and load-bearing, both gates (alone and
// together) must exit 0 and write nothing to stderr.
func TestScanGate_PassExitCode(t *testing.T) {
	for _, flags := range [][]string{
		{"--require-5of5"},
		{"--require-loadbearing"},
		{"--require-5of5", "--require-loadbearing"},
	} {
		common := scaffoldAllPass(t)
		args := append(append([]string{"scan", "--no-scanned-at"}, flags...), common...)

		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitOK {
			t.Errorf("%v: got exit %d want %d (OK); stderr=%q", flags, code, exitOK, errb.String())
		}
		if strings.Contains(errb.String(), "exit-gate") {
			t.Errorf("%v: gate passed but emitted a diagnostic:\n%s", flags, errb.String())
		}
		if !strings.Contains(out.String(), `name: "recall"`) {
			t.Errorf("%v: snapshot not emitted on gate pass:\n%s", flags, out.String())
		}
	}
}

// TestScanGate_NoFlagsUnchanged confirms the default (no gate flags) path is
// untouched: plain scan exits 0 even though scaffoldTree has components
// below the bar.
func TestScanGate_NoFlagsUnchanged(t *testing.T) {
	_, common := scaffoldTree(t)
	var out, errb bytes.Buffer
	if code := run(append([]string{"scan", "--no-scanned-at"}, common...), &out, &errb); code != exitOK {
		t.Fatalf("plain scan (no gate): got exit %d want %d; stderr=%q", code, exitOK, errb.String())
	}
	if strings.Contains(errb.String(), "exit-gate") {
		t.Errorf("plain scan emitted a gate diagnostic with no gate flag:\n%s", errb.String())
	}
}

// TestScanGate_FailClosedOnEmptyScan covers the fail-closed branch: a gate
// requested over zero scanned components must return exitGate rather than
// vacuously passing (a mis-pointed root must not silently clear the bar).
func TestScanGate_FailClosedOnEmptyScan(t *testing.T) {
	root := t.TempDir()
	common := []string{
		"--infra-dir=",
		"--engines-dir=",
		"--foundation-dir=",
		"--flagships-dir=" + filepath.Join(root, "flagships"),
		"--checkout-root=" + root,
	}
	args := append([]string{"scan", "--no-scanned-at", "--require-5of5"}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitGate {
		t.Fatalf("empty scan + gate: got exit %d want %d (fail-closed); stderr=%q", code, exitGate, errb.String())
	}
	if !strings.Contains(errb.String(), "no components were scanned") {
		t.Errorf("expected fail-closed message, got stderr:\n%s", errb.String())
	}
}

// TestScanGate_StdoutByteIdenticalWithGate locks in that requesting a gate
// does not change stdout (the snapshot) — the diagnostic is stderr-only —
// so downstream consumers see the same bytes whether or not a gate is set.
func TestScanGate_StdoutByteIdenticalWithGate(t *testing.T) {
	_, common := scaffoldTree(t)
	base := append([]string{"scan", "--format=json", "--no-scanned-at"}, common...)
	gated := append([]string{"scan", "--format=json", "--no-scanned-at", "--require-5of5"}, common...)

	var outBase, outGated, errb bytes.Buffer
	if code := run(base, &outBase, &errb); code != exitOK {
		t.Fatalf("base scan: exit %d", code)
	}
	if code := run(gated, &outGated, &errb); code != exitGate {
		t.Fatalf("gated scan: got exit %d want %d (gate)", code, exitGate)
	}
	if !bytes.Equal(outBase.Bytes(), outGated.Bytes()) {
		t.Errorf("stdout differs with --require-5of5:\n--- base ---\n%s\n--- gated ---\n%s", outBase.String(), outGated.String())
	}
}

// TestScanGate_GateOverridesFileWriteSuccess verifies the gate exit code
// wins even when the snapshot is written to a file (writeOut returns OK for
// a successful file write; the gate must still surface exitGate).
func TestScanGate_GateOverridesFileWriteSuccess(t *testing.T) {
	_, common := scaffoldTree(t)
	outFile := filepath.Join(t.TempDir(), "snap.yaml")
	args := append([]string{"scan", "--no-scanned-at", "--require-5of5", "--out=" + outFile}, common...)

	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != exitGate {
		t.Fatalf("gate over file write: got exit %d want %d (gate); stderr=%q", code, exitGate, errb.String())
	}
	// The file must still have been written (report-then-fail).
	if b, err := os.ReadFile(outFile); err != nil {
		t.Fatalf("snapshot file not written before gate failure: %v", err)
	} else if !strings.Contains(string(b), `name: "recall"`) {
		t.Errorf("written snapshot missing recall:\n%s", b)
	}
}

// TestRun_FormatAndParseErrors covers the invalid-format and
// unparseable-flag exit paths in cmdRender / cmdScan.
func TestRun_FormatAndParseErrors(t *testing.T) {
	_, common := scaffoldTree(t)
	cases := []struct {
		name string
		args []string
	}{
		{"render-bad-format", append([]string{"render", "--out=-", "--format=xml"}, common...)},
		{"scan-bad-format", append([]string{"scan", "--format=xml"}, common...)},
		{"render-bad-flag", append([]string{"render", "--width=notanint"}, common...)},
		{"scan-unknown-flag", append([]string{"scan", "--bogus=1"}, common...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if code := run(tc.args, &out, &errb); code != exitUsage {
				t.Errorf("got exit %d want %d (usage); stderr=%q", code, exitUsage, errb.String())
			}
		})
	}
}
