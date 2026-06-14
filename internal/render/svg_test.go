package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/davly/infra-cohort-map/internal/relate"
	"github.com/davly/infra-cohort-map/internal/scanner"
)

func sampleSnapshot() scanner.Snapshot {
	return scanner.Snapshot{
		GeneratedAt: "2026-05-28T00:00:00Z",
		Components: []scanner.Component{
			{
				Name:          "recall",
				Layer:         scanner.LayerInfrastructure,
				Substrate:     "go",
				PackageStatus: [5]bool{true, true, true, true, true},
				CohortCount:   5,
				LoadBearing:   true,
				KAT1Pinned:    true,
			},
			{
				Name:          "codex",
				Layer:         scanner.LayerInfrastructure,
				Substrate:     "go",
				PackageStatus: [5]bool{false, false, false, false, false},
				CohortCount:   0,
			},
			{
				Name:          "causal",
				Layer:         scanner.LayerEngine,
				Substrate:     "go",
				PackageStatus: [5]bool{true, false, true, false, true},
				CohortCount:   3,
				InternalDeps:  []string{"recall"},
			},
			{
				Name:          "aicore",
				Layer:         scanner.LayerFoundation,
				Substrate:     "go",
				PackageStatus: [5]bool{false, false, false, false, false},
				CohortCount:   0,
			},
		},
	}
}

func sampleConsumers() relate.Consumers {
	return relate.Consumers{
		"recall": {"academy", "arbiter", "barista"},
		"codex":  {"barista"},
		"causal": {"academy"},
		"aicore": {"academy", "arbiter"},
	}
}

func TestRender_EmitsValidSVGEnvelope(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	s := string(out)
	if !strings.HasPrefix(s, "<?xml") {
		t.Fatal("missing XML declaration")
	}
	if !strings.Contains(s, "<svg xmlns=\"http://www.w3.org/2000/svg\"") {
		t.Fatal("missing svg root")
	}
	if !strings.HasSuffix(strings.TrimRight(s, "\n"), "</svg>") {
		t.Fatalf("svg not closed; tail = %q", s[max(0, len(s)-40):])
	}
}

func TestRender_ContainsAllComponentNames(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	for _, name := range []string{"recall", "codex", "causal", "aicore"} {
		if !bytes.Contains(out, []byte(">"+name+"<")) {
			t.Errorf("component label %q missing", name)
		}
	}
}

func TestRender_LoadBearingRendersThickStroke(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	// recall is load-bearing → stroke-width="4"
	if !bytes.Contains(out, []byte(`stroke-width="4"`)) {
		t.Fatal("expected stroke-width=4 for load-bearing node")
	}
}

func TestRender_KAT1PinnedRendersHalo(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	// recall is KAT-1 pinned → dashed amber halo
	if !bytes.Contains(out, []byte(`stroke="#F59E0B"`)) {
		t.Fatal("expected amber halo for KAT-1 pinned node")
	}
	if !bytes.Contains(out, []byte(`stroke-dasharray="3 3"`)) {
		t.Fatal("expected dashed halo")
	}
}

func TestRender_PipsRenderBothColours(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	// At least one green pip (#10B981) and one gray pip (#9CA3AF) should appear.
	if !bytes.Contains(out, []byte(`fill="#10B981"`)) {
		t.Fatal("expected green pip somewhere")
	}
	if !bytes.Contains(out, []byte(`fill="#9CA3AF"`)) {
		t.Fatal("expected gray pip somewhere")
	}
}

func TestRender_ProjectionStampAppears(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{Projection: "projected"})
	if !bytes.Contains(out, []byte("projection: projected")) {
		t.Fatal("projected banner missing")
	}
}

func TestRender_SnapshotDateAppears(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{Snapshot: "2026-05-28"})
	if !bytes.Contains(out, []byte("2026-05-28")) {
		t.Fatal("snapshot date missing")
	}
}

func TestRender_EdgeDrawnForCrossInfraDep(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	// causal has internal_deps: [recall] → expect a <line ... marker-end=arrow>
	if !bytes.Contains(out, []byte(`marker-end="url(#arrow)"`)) {
		t.Fatal("expected dependency edge with arrow marker")
	}
}

func TestRender_LegendIncludesSubstrateSwatches(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	if !bytes.Contains(out, []byte(">go<")) {
		t.Error("legend missing go swatch label")
	}
	if !bytes.Contains(out, []byte(">python<")) {
		t.Error("legend missing python swatch label")
	}
}

func TestRender_KAT1HexInFooter(t *testing.T) {
	out := Render(sampleSnapshot(), sampleConsumers(), Options{})
	// First 16 chars of canonical hex should appear in footer.
	if !bytes.Contains(out, []byte(scanner.KAT1HexCanonical[:16])) {
		t.Fatal("KAT-1 hex prefix missing from footer")
	}
}

func TestRender_NodeRadiusScalesWithConsumers(t *testing.T) {
	r0 := nodeRadius(0)
	r1 := nodeRadius(1)
	r10 := nodeRadius(10)
	if !(r0 < r10) {
		t.Fatalf("radius did not scale: r0=%d r10=%d", r0, r10)
	}
	if r1 < r0 {
		t.Fatalf("radius decreased: r0=%d r1=%d", r0, r1)
	}
	// radius cap
	if nodeRadius(10000) > 50 {
		t.Fatalf("radius cap violated: %d", nodeRadius(10000))
	}
}

func TestXMLEscape(t *testing.T) {
	cases := map[string]string{
		`<svg>`:     `&lt;svg&gt;`,
		`a & b`:     `a &amp; b`,
		`"quoted"`:  `&quot;quoted&quot;`,
		`it's fine`: `it&apos;s fine`,
	}
	for in, want := range cases {
		got := xmlEscape(in)
		if got != want {
			t.Errorf("xmlEscape(%q): got %q want %q", in, got, want)
		}
	}
}

func TestRender_EmptySnapshotIsValid(t *testing.T) {
	out := Render(scanner.Snapshot{}, relate.Consumers{}, Options{})
	if !bytes.Contains(out, []byte("</svg>")) {
		t.Fatal("empty snapshot did not produce closed SVG")
	}
}

// eightComponentSnapshot returns a snapshot with 8 components spread across
// all three layers so several share a lane — the map-iteration ordering bug
// surfaces most reliably when a lane holds multiple nodes.
func eightComponentSnapshot() scanner.Snapshot {
	mk := func(name string, layer scanner.Layer, deps ...string) scanner.Component {
		return scanner.Component{
			Name:          name,
			Layer:         layer,
			Substrate:     "go",
			PackageStatus: [5]bool{true, false, true, false, true},
			CohortCount:   3,
			InternalDeps:  deps,
		}
	}
	return scanner.Snapshot{
		GeneratedAt: "2026-06-14T00:00:00Z",
		Components: []scanner.Component{
			mk("recall", scanner.LayerInfrastructure),
			mk("codex", scanner.LayerInfrastructure),
			mk("vault", scanner.LayerInfrastructure),
			mk("causal", scanner.LayerEngine, "recall"),
			mk("oracle", scanner.LayerEngine, "vault"),
			mk("parallax", scanner.LayerEngine),
			mk("aicore", scanner.LayerFoundation),
			mk("bedrock", scanner.LayerFoundation),
		},
	}
}

func eightConsumers() relate.Consumers {
	return relate.Consumers{
		"recall":   {"academy", "arbiter", "barista"},
		"codex":    {"barista"},
		"vault":    {"academy", "arbiter"},
		"causal":   {"academy"},
		"oracle":   {"arbiter", "barista"},
		"parallax": {"academy"},
		"aicore":   {"academy", "arbiter"},
		"bedrock":  {"barista"},
	}
}

// TestRender_DeterministicOutput is the discriminating regression test for the
// non-deterministic node-draw loop. Before the fix Render ranged over the
// positions map directly, so Go's randomized map iteration order emitted the
// per-node <circle>/<text> lines in a different order on most runs for
// byte-identical input. After the fix the keys are sorted before iteration, so
// every render of the same snapshot is byte-identical.
//
// This is a genuine fail-before/pass-after test, not a tautology: it was
// confirmed to FAIL on the pre-fix code (the unsorted map range) by reverting
// the production change, and to PASS once the sort was restored.
func TestRender_DeterministicOutput(t *testing.T) {
	snap := eightComponentSnapshot()
	cons := eightConsumers()
	opts := Options{Snapshot: "2026-06-14", Provenance: "determinism-test"}

	const n = 50
	first := Render(snap, cons, opts)
	for i := 1; i < n; i++ {
		got := Render(snap, cons, opts)
		if !bytes.Equal(first, got) {
			// Surface the first differing line to make a regression obvious.
			fl := strings.Split(string(first), "\n")
			gl := strings.Split(string(got), "\n")
			diffLine := -1
			for li := 0; li < len(fl) && li < len(gl); li++ {
				if fl[li] != gl[li] {
					diffLine = li
					break
				}
			}
			if diffLine >= 0 {
				t.Fatalf("Render output not deterministic: run %d/%d differs from run 0 at line %d:\n  first: %q\n  got:   %q",
					i, n, diffLine, fl[diffLine], gl[diffLine])
			}
			t.Fatalf("Render output not deterministic: run %d/%d differs from run 0 (length %d vs %d)",
				i, n, len(first), len(got))
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
