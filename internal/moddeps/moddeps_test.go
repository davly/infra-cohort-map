package moddeps

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeGoMod writes dir/go.mod with body, creating parents.
func writeGoMod(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644); err != nil {
		t.Fatalf("write go.mod %s: %v", dir, err)
	}
}

// layout builds the four canonical roots under a temp dir and returns them
// plus the aicore producer root (single-component foundation layout).
func layout(t *testing.T) (roots []SearchRoot, infra, engines, flagships, aicoreRoot string) {
	t.Helper()
	base := t.TempDir()
	infra = filepath.Join(base, "infrastructure")
	engines = filepath.Join(base, "engines")
	foundation := filepath.Join(base, "foundation", "aicore")
	flagships = filepath.Join(base, "flagships")
	for _, d := range []string{infra, engines, foundation, flagships} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	roots = []SearchRoot{
		{Dir: infra, SingleComponent: false},
		{Dir: engines, SingleComponent: false},
		{Dir: foundation, SingleComponent: true},
		{Dir: flagships, SingleComponent: false},
	}
	return roots, infra, engines, flagships, foundation
}

// TestRequireBasedConsumption: a plain `require M` records the consumer.
func TestRequireBasedConsumption(t *testing.T) {
	roots, infra, _, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	writeGoMod(t, filepath.Join(infra, "lore"), "module example.com/lore\n\ngo 1.22\n")
	// nexus requires lore (single-line require form).
	writeGoMod(t, filepath.Join(infra, "nexus"),
		"module example.com/nexus\n\ngo 1.22\n\nrequire example.com/lore v0.1.0\n")
	// a flagship requires aicore via a require block.
	writeGoMod(t, filepath.Join(flagships, "ouroboros"),
		"module example.com/ouroboros\n\ngo 1.22\n\nrequire (\n\texample.com/aicore v0.8.0\n)\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
		{Name: "lore", ModulePath: "example.com/lore", RootDir: filepath.Join(infra, "lore")},
		{Name: "nexus", ModulePath: "example.com/nexus", RootDir: filepath.Join(infra, "nexus")},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if !reflect.DeepEqual(got["lore"], []string{"nexus"}) {
		t.Errorf("lore consumers: got %v want [nexus]", got["lore"])
	}
	if !reflect.DeepEqual(got["aicore"], []string{"ouroboros"}) {
		t.Errorf("aicore consumers: got %v want [ouroboros]", got["aicore"])
	}
	if got["nexus"] != nil {
		t.Errorf("nexus consumers: got %v want nil", got["nexus"])
	}
}

// TestReplaceBasedConsumption: a replace directive whose TARGET local path
// resolves to the producer's root dir counts, even with no matching require
// module path in the producer set (path form).
func TestReplaceBasedConsumption(t *testing.T) {
	roots, _, _, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	// pushforge replaces a DIFFERENT module name onto aicore's local dir —
	// the module path does not match, but the target path does. Both block
	// and single-line replace forms are exercised across the two flagships.
	writeGoMod(t, filepath.Join(flagships, "pushforge"),
		"module example.com/pushforge\n\ngo 1.22\n\n"+
			"require example.com/vendored-alias v0.0.0\n\n"+
			"replace example.com/vendored-alias => ../../foundation/aicore\n")
	writeGoMod(t, filepath.Join(flagships, "triage"),
		"module example.com/triage\n\ngo 1.22\n\n"+
			"require (\n\texample.com/some-fork v0.0.0\n)\n\n"+
			"replace (\n\texample.com/some-fork => ../../foundation/aicore\n)\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	want := []string{"pushforge", "triage"}
	if !reflect.DeepEqual(got["aicore"], want) {
		t.Errorf("aicore replace-target consumers: got %v want %v", got["aicore"], want)
	}
}

// TestReplaceModulePathReplacementNotCounted: a replace of one module path
// onto ANOTHER module path (not a local dir) is not a local-path consumption
// and must not spuriously count.
func TestReplaceModulePathReplacementNotCounted(t *testing.T) {
	roots, infra, _, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	writeGoMod(t, filepath.Join(infra, "lore"), "module example.com/lore\n\ngo 1.22\n")
	// A module->module replace (no local path); must not be read as consuming lore.
	writeGoMod(t, filepath.Join(flagships, "alpha"),
		"module example.com/alpha\n\ngo 1.22\n\nreplace example.com/lore => example.com/lore-fork v1.2.3\n")

	producers := []Producer{
		{Name: "lore", ModulePath: "example.com/lore", RootDir: filepath.Join(infra, "lore")},
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if got["lore"] != nil {
		t.Errorf("module->module replace wrongly counted: got %v want nil", got["lore"])
	}
}

// TestSelfExclusion: a producer that requires/replaces itself is never its
// own consumer.
func TestSelfExclusion(t *testing.T) {
	roots, infra, _, _, aicoreRoot := layout(t)
	// aicore's own go.mod (self) — even if it names its own module path.
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n\nrequire example.com/aicore v0.0.0\n")
	// lore requires itself in an odd manifest AND replaces to its own dir.
	loreDir := filepath.Join(infra, "lore")
	writeGoMod(t, loreDir,
		"module example.com/lore\n\ngo 1.22\n\nrequire example.com/lore v0.0.0\n\nreplace example.com/lore => .\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
		{Name: "lore", ModulePath: "example.com/lore", RootDir: loreDir},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if got["aicore"] != nil {
		t.Errorf("aicore self-counted: got %v want nil", got["aicore"])
	}
	if got["lore"] != nil {
		t.Errorf("lore self-counted (require+replace-to-self): got %v want nil", got["lore"])
	}
}

// TestNoConsumers: producers nobody requires map to a nil slice (present key,
// deterministic zero).
func TestNoConsumers(t *testing.T) {
	roots, infra, _, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	writeGoMod(t, filepath.Join(infra, "orphan"), "module example.com/orphan\n\ngo 1.22\n")
	// a flagship that requires something entirely unrelated.
	writeGoMod(t, filepath.Join(flagships, "alpha"),
		"module example.com/alpha\n\ngo 1.22\n\nrequire example.com/unrelated v1.0.0\n")

	producers := []Producer{
		{Name: "orphan", ModulePath: "example.com/orphan", RootDir: filepath.Join(infra, "orphan")},
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if _, ok := got["orphan"]; !ok {
		t.Fatal("orphan key missing from result")
	}
	if got["orphan"] != nil {
		t.Errorf("orphan consumers: got %v want nil", got["orphan"])
	}
	if got["aicore"] != nil {
		t.Errorf("aicore consumers: got %v want nil", got["aicore"])
	}
}

// TestNestedGoModOwnerAttribution: a go.mod nested under a component (the
// nexus src/api pattern) is attributed to the top-level component, not the
// nested dir name.
func TestNestedGoModOwnerAttribution(t *testing.T) {
	roots, infra, _, _, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	// nexus keeps its module at infrastructure/nexus/src/api/go.mod.
	writeGoMod(t, filepath.Join(infra, "nexus", "src", "api"),
		"module example.com/nexus/api\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if !reflect.DeepEqual(got["aicore"], []string{"nexus"}) {
		t.Errorf("nested go.mod owner: got %v want [nexus]", got["aicore"])
	}
}

// TestVendorAndTestdataSkipped: go.mod files under vendor/ or testdata/ are
// not scanned as consumers.
func TestVendorAndTestdataSkipped(t *testing.T) {
	roots, _, _, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	// A real consumer.
	writeGoMod(t, filepath.Join(flagships, "real"),
		"module example.com/real\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n")
	// A vendored copy and a testdata copy that also require aicore — must be ignored.
	writeGoMod(t, filepath.Join(flagships, "real", "vendor", "example.com", "aicore"),
		"module example.com/aicore\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n")
	writeGoMod(t, filepath.Join(flagships, "decoy", "testdata", "sample"),
		"module example.com/sample\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if !reflect.DeepEqual(got["aicore"], []string{"real"}) {
		t.Errorf("vendor/testdata not skipped: got %v want [real]", got["aicore"])
	}
}

// TestInversionAgainstCensus is the documentary regression: a small tree
// shaped like the audit finding — a "hub" required by many modules but hosting
// no same-named dir (census 0) and a "vendored" component pattern-copied into
// many flagships but required by exactly one module. moddeps must report the
// TRUE module degree, inverting the (separately-computed) census.
func TestInversionAgainstCensus(t *testing.T) {
	roots, infra, engines, flagships, aicoreRoot := layout(t)
	writeGoMod(t, aicoreRoot, "module example.com/aicore\n\ngo 1.22\n")
	loreDir := filepath.Join(infra, "lore")
	writeGoMod(t, loreDir, "module example.com/lore\n\ngo 1.22\n")
	nexusDir := filepath.Join(infra, "nexus")

	// Hub: three infra + one engine + two flagships all require aicore.
	for _, d := range []string{
		filepath.Join(infra, "conduit"),
		filepath.Join(infra, "recall"),
		nexusDir,
		filepath.Join(engines, "causal"),
		filepath.Join(flagships, "ouroboros"),
		filepath.Join(flagships, "pushforge"),
	} {
		writeGoMod(t, d, "module example.com/"+filepath.Base(d)+
			"\n\ngo 1.22\n\nrequire example.com/aicore v0.8.0\n\nreplace example.com/aicore => "+
			relTo(d, aicoreRoot)+"\n")
	}
	// lore is required by exactly one module (nexus) — matching the estate.
	writeGoMod(t, nexusDir, "module example.com/nexus\n\ngo 1.22\n\n"+
		"require example.com/aicore v0.8.0\nrequire example.com/lore v0.1.0\n\n"+
		"replace example.com/aicore => "+relTo(nexusDir, aicoreRoot)+"\n")

	producers := []Producer{
		{Name: "aicore", ModulePath: "example.com/aicore", RootDir: aicoreRoot},
		{Name: "lore", ModulePath: "example.com/lore", RootDir: loreDir},
	}
	got, err := CountModuleConsumers(roots, producers)
	if err != nil {
		t.Fatalf("CountModuleConsumers: %v", err)
	}
	if n := len(got["aicore"]); n != 6 {
		t.Errorf("aicore module_consumer_count: got %d (%v) want 6", n, got["aicore"])
	}
	if !reflect.DeepEqual(got["lore"], []string{"nexus"}) {
		t.Errorf("lore module consumers: got %v want [nexus]", got["lore"])
	}
	// Determinism: identical across runs.
	got2, _ := CountModuleConsumers(roots, producers)
	if !reflect.DeepEqual(got, got2) {
		t.Error("CountModuleConsumers not deterministic across runs")
	}
}

// relTo returns a forward-slash relative path from a go.mod dir to target,
// as it would appear in a replace directive.
func relTo(from, target string) string {
	r, err := filepath.Rel(from, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(r)
}
