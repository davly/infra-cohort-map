package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"version", []string{"--version"}, exitOK, version, ""},
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

// TestRun_VersionExact pins the --version output to exactly the version
// constant plus a newline (regulator automation may parse it).
func TestRun_VersionExact(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb); code != exitOK {
		t.Fatalf("--version: exit %d", code)
	}
	if got, want := out.String(), version+"\n"; got != want {
		t.Errorf("--version: got %q want %q", got, want)
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
