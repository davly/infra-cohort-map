// Package render produces SVG output for an infra cohort snapshot.
// Pure stdlib (encoding/xml + fmt). The output is line-oriented so it
// diff-reviews cleanly in a PR comment.
//
// Layout:
//
//	+---------------------------------------------------------------+
//	|  Title bar (Title / Subtitle / SnapshotDate / KAT-1 hex)      |
//	+---------------------------------------------------------------+
//	|                                                               |
//	|  three horizontal lanes (foundation / engines / infrastructure)|
//	|  each lane: nodes laid out left-to-right                       |
//	|    node radius scales with consumer-flagship count             |
//	|    node fill = substrate colour                                |
//	|    node stroke width = load-bearing? thick : thin              |
//	|    node halo = KAT-1 pinned? dashed circle : none              |
//	|    five pips under each node = R174 cohort presence            |
//	|  edges drawn between cross-infra deps                          |
//	+---------------------------------------------------------------+
//	|  Legend + provenance footer                                   |
//	+---------------------------------------------------------------+
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/davly/infra-cohort-map/internal/relate"
	"github.com/davly/infra-cohort-map/internal/scanner"
)

// Options controls renderer behaviour.
type Options struct {
	Width  int
	Height int

	Title      string
	Subtitle   string
	Snapshot   string // human date stamp shown in the title bar
	Projection string // "current" or "projected" — banner stamp
	Provenance string // free-form provenance footer line
}

// Defaults applies zero-value defaults.
func (o Options) Defaults() Options {
	if o.Width <= 0 {
		o.Width = 1800
	}
	if o.Height <= 0 {
		o.Height = 1100
	}
	if o.Title == "" {
		o.Title = "Limitless Infrastructure Cohort Map"
	}
	return o
}

// Substrate colour palette — mirrors the flagship cohort-map palette
// so visual cross-reference works.
var substrateFill = map[string]string{
	"go":         "#0F766E", // teal-700
	"python":     "#1E3A8A", // indigo-800
	"rust":       "#9A3412", // orange-800
	"crystal":    "#581C87", // purple-900
	"typescript": "#1D4ED8", // blue-700
	"javascript": "#F59E0B", // amber-500
	"c":          "#374151", // gray-700
	"elixir":     "#7C2D12", // amber-900
	"unknown":    "#6B7280", // gray-500
}

// Render emits the SVG bytes for a snapshot + consumer map.
func Render(snap scanner.Snapshot, consumers relate.Consumers, opts Options) []byte {
	o := opts.Defaults()
	var sb strings.Builder
	header(&sb, o)
	titleBar(&sb, o)
	defs(&sb)

	// Group components by layer for the three-lane layout.
	groups := groupByLayer(snap.Components)
	layerOrder := []scanner.Layer{scanner.LayerFoundation, scanner.LayerEngine, scanner.LayerInfrastructure}

	// Layout box for the swimlanes (between title bar and footer).
	laneTop := 110
	laneBottom := o.Height - 130
	laneHeight := (laneBottom - laneTop) / len(layerOrder)

	// Node positions, used afterwards for edge drawing.
	positions := map[string]pos{}

	for li, layer := range layerOrder {
		comps := groups[layer]
		laneY := laneTop + li*laneHeight
		laneLabel(&sb, layer, laneY, laneHeight, o.Width)

		// Layout: a grid inside the width minus the 200px lane label band.
		bandLeft := 200
		bandRight := o.Width - 40
		bandW := bandRight - bandLeft
		n := len(comps)
		if n == 0 {
			continue
		}
		cells := layoutLane(n, bandLeft, bandW, laneY, laneHeight)
		for ci, c := range comps {
			positions[c.Name] = pos{
				x:    cells[ci].cx,
				y:    cells[ci].cy,
				r:    nodeRadius(len(consumers[c.Name])),
				comp: c,
			}
		}
	}

	// Draw edges first (so nodes sit on top).
	edges(&sb, snap.Components, positions)

	// Draw nodes. Iterate positions in a stable (sorted-key) order so the
	// emitted <circle>/<text> lines are byte-identical run-to-run for the
	// same input — Go randomizes map iteration order, which would otherwise
	// break the package's line-oriented diff-review / regulator-reproducible
	// contract. The rendered image is unaffected (same nodes/coords/radii).
	nodeKeys := make([]string, 0, len(positions))
	for name := range positions {
		nodeKeys = append(nodeKeys, name)
	}
	sort.Strings(nodeKeys)
	for _, name := range nodeKeys {
		p := positions[name]
		drawNode(&sb, p.x, p.y, p.r, p.comp, len(consumers[p.comp.Name]))
	}

	legend(&sb, o)
	footer(&sb, o)
	closeSVG(&sb)
	return []byte(sb.String())
}

// laneCell is a computed grid slot (node center) within a lane.
type laneCell struct{ cx, cy int }

// minCellW is the minimum horizontal room a node gets in a lane grid. It
// comfortably exceeds the maximum node diameter (2*44=88) plus a label
// gutter, so adjacent nodes in a row never collide.
const minCellW = 120

// layoutLane arranges n nodes into a grid inside the lane so each node
// gets at least minCellW horizontal room, wrapping into multiple rows
// when a single row would pack them tighter than that. This stops the
// dense ~36-node infrastructure lane from overlapping. When n fits in a
// single row the result is one centered row identical to the legacy
// even-spread layout (so small-lane output is byte-stable).
func layoutLane(n, bandLeft, bandW, laneTop, laneHeight int) []laneCell {
	if n <= 0 {
		return nil
	}
	perRow := bandW / minCellW
	if perRow < 1 {
		perRow = 1
	}
	if perRow > n {
		perRow = n
	}
	rows := (n + perRow - 1) / perRow
	cellW := bandW / perRow
	rowH := laneHeight / rows
	cells := make([]laneCell, n)
	for i := 0; i < n; i++ {
		row := i / perRow
		col := i % perRow
		cells[i] = laneCell{
			cx: bandLeft + col*cellW + cellW/2,
			cy: laneTop + row*rowH + rowH/2,
		}
	}
	return cells
}

func groupByLayer(comps []scanner.Component) map[scanner.Layer][]scanner.Component {
	g := map[scanner.Layer][]scanner.Component{}
	for _, c := range comps {
		g[c.Layer] = append(g[c.Layer], c)
	}
	for k := range g {
		sort.Slice(g[k], func(i, j int) bool { return g[k][i].Name < g[k][j].Name })
	}
	return g
}

func header(sb *strings.Builder, o Options) {
	fmt.Fprintf(sb, `<?xml version="1.0" encoding="UTF-8" standalone="no"?>`+"\n")
	fmt.Fprintf(sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family="-apple-system,Segoe UI,Helvetica,Arial,sans-serif">`+"\n",
		o.Width, o.Height, o.Width, o.Height)
	// Background.
	fmt.Fprintf(sb, `  <rect width="100%%" height="100%%" fill="#F9FAFB"/>`+"\n")
}

func defs(sb *strings.Builder) {
	// Edge arrow marker.
	fmt.Fprintf(sb, `  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5"
            markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M 0 0 L 10 5 L 0 10 z" fill="#374151"/>
    </marker>
  </defs>`+"\n")
}

func titleBar(sb *strings.Builder, o Options) {
	fmt.Fprintf(sb, `  <rect x="0" y="0" width="%d" height="80" fill="#111827"/>`+"\n", o.Width)
	fmt.Fprintf(sb, `  <text x="40" y="40" fill="#F9FAFB" font-size="28" font-weight="700">%s</text>`+"\n", xmlEscape(o.Title))
	if o.Subtitle != "" {
		fmt.Fprintf(sb, `  <text x="40" y="65" fill="#D1D5DB" font-size="14">%s</text>`+"\n", xmlEscape(o.Subtitle))
	}
	if o.Snapshot != "" {
		fmt.Fprintf(sb, `  <text x="%d" y="40" fill="#D1D5DB" font-size="14" text-anchor="end">%s</text>`+"\n",
			o.Width-40, xmlEscape(o.Snapshot))
	}
	if o.Projection != "" {
		bg := "#065F46"
		if o.Projection == "projected" {
			bg = "#7C2D12"
		}
		fmt.Fprintf(sb, `  <rect x="%d" y="50" width="180" height="22" fill="%s" rx="3"/>`+"\n",
			o.Width-220, bg)
		fmt.Fprintf(sb, `  <text x="%d" y="66" fill="#F9FAFB" font-size="12" font-weight="600" text-anchor="middle">%s</text>`+"\n",
			o.Width-130, "projection: "+xmlEscape(o.Projection))
	}
}

func laneLabel(sb *strings.Builder, layer scanner.Layer, y, h, width int) {
	fmt.Fprintf(sb, `  <rect x="20" y="%d" width="160" height="%d" fill="#E5E7EB" rx="4"/>`+"\n", y+10, h-20)
	fmt.Fprintf(sb, `  <text x="100" y="%d" fill="#1F2937" font-size="16" font-weight="700" text-anchor="middle">%s</text>`+"\n",
		y+h/2-4, xmlEscape(string(layer)))
	fmt.Fprintf(sb, `  <text x="100" y="%d" fill="#6B7280" font-size="10" text-anchor="middle">layer</text>`+"\n", y+h/2+12)
	// Soft horizontal divider.
	fmt.Fprintf(sb, `  <line x1="200" y1="%d" x2="%d" y2="%d" stroke="#E5E7EB" stroke-width="1" stroke-dasharray="4 4"/>`+"\n",
		y+h, width-20, y+h)
}

// nodeRadius scales radius to consumer count using a sqrt curve to
// avoid runaway size for very popular nodes.
func nodeRadius(consumers int) int {
	const base = 14
	const cap = 44
	r := base + int(0.7*sqrtish(float64(consumers)*9))
	if r > cap {
		r = cap
	}
	return r
}

// sqrtish — fast integer-ish sqrt that doesn't need math.Sqrt for our
// scale.  We accept ±1 fuzz at the high end.
func sqrtish(x float64) float64 {
	if x <= 0 {
		return 0
	}
	g := x / 2
	for i := 0; i < 12; i++ {
		g = (g + x/g) / 2
	}
	return g
}

func drawNode(sb *strings.Builder, cx, cy, r int, c scanner.Component, consumers int) {
	fill := substrateFill[c.Substrate]
	if fill == "" {
		fill = substrateFill["unknown"]
	}
	strokeW := 1
	stroke := "#374151"
	if c.LoadBearing {
		strokeW = 4
		stroke = "#10B981"
	}
	// KAT-1 halo.
	if c.KAT1Pinned {
		fmt.Fprintf(sb, `  <circle cx="%d" cy="%d" r="%d" fill="none" stroke="#F59E0B" stroke-width="2" stroke-dasharray="3 3"/>`+"\n",
			cx, cy, r+6)
	}
	fmt.Fprintf(sb, `  <circle cx="%d" cy="%d" r="%d" fill="%s" stroke="%s" stroke-width="%d"/>`+"\n",
		cx, cy, r, fill, stroke, strokeW)
	// Name label below pips.
	fmt.Fprintf(sb, `  <text x="%d" y="%d" fill="#111827" font-size="11" font-weight="600" text-anchor="middle">%s</text>`+"\n",
		cx, cy+r+22, xmlEscape(c.Name))
	// Consumer-count badge centred on node.
	fmt.Fprintf(sb, `  <text x="%d" y="%d" fill="#F9FAFB" font-size="11" font-weight="700" text-anchor="middle">%d</text>`+"\n",
		cx, cy+4, consumers)
	// 5-pip indicator.
	drawPips(sb, cx, cy+r+30, c.PackageStatus)
	// Substrate micro-label.
	fmt.Fprintf(sb, `  <text x="%d" y="%d" fill="#6B7280" font-size="9" text-anchor="middle">%s</text>`+"\n",
		cx, cy+r+50, xmlEscape(c.Substrate))
}

func drawPips(sb *strings.Builder, cx, baseY int, status [5]bool) {
	const pipR = 4
	const pipGap = 11
	startX := cx - 2*pipGap
	for i, on := range status {
		fill := "#9CA3AF" // gray
		if on {
			fill = "#10B981" // green-500
		}
		fmt.Fprintf(sb, `  <circle cx="%d" cy="%d" r="%d" fill="%s"/>`+"\n",
			startX+i*pipGap, baseY, pipR, fill)
	}
}

// pos is the on-canvas placement of a scanned component. Used by
// edge-drawing to look up endpoints.
type pos struct {
	x, y, r int
	comp    scanner.Component
}

func edges(sb *strings.Builder, comps []scanner.Component, positions map[string]pos) {
	for _, c := range comps {
		for _, dep := range c.InternalDeps {
			a, ok := positions[c.Name]
			if !ok {
				continue
			}
			b, ok := positions[dep]
			if !ok {
				continue
			}
			fmt.Fprintf(sb, `  <line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#374151" stroke-width="1" stroke-opacity="0.45" marker-end="url(#arrow)"/>`+"\n",
				a.x, a.y, b.x, b.y)
		}
	}
}

func legend(sb *strings.Builder, o Options) {
	y := o.Height - 110
	fmt.Fprintf(sb, `  <text x="40" y="%d" fill="#1F2937" font-size="13" font-weight="700">Legend</text>`+"\n", y)

	// Substrate swatches.
	keys := make([]string, 0, len(substrateFill))
	for k := range substrateFill {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	x := 110
	for _, k := range keys {
		fmt.Fprintf(sb, `  <rect x="%d" y="%d" width="12" height="12" fill="%s" rx="2"/>`+"\n",
			x, y-10, substrateFill[k])
		fmt.Fprintf(sb, `  <text x="%d" y="%d" fill="#1F2937" font-size="11">%s</text>`+"\n",
			x+16, y, xmlEscape(k))
		x += 100
	}

	// Pip key.
	pipY := y + 24
	fmt.Fprintf(sb, `  <text x="40" y="%d" fill="#1F2937" font-size="11" font-weight="600">R174 5-of-5:</text>`+"\n", pipY)
	fmt.Fprintf(sb, `  <circle cx="160" cy="%d" r="4" fill="#10B981"/>`+"\n", pipY-4)
	fmt.Fprintf(sb, `  <text x="172" y="%d" font-size="10" fill="#1F2937">present</text>`+"\n", pipY)
	fmt.Fprintf(sb, `  <circle cx="240" cy="%d" r="4" fill="#9CA3AF"/>`+"\n", pipY-4)
	fmt.Fprintf(sb, `  <text x="252" y="%d" font-size="10" fill="#1F2937">absent</text>`+"\n", pipY)
	fmt.Fprintf(sb, `  <text x="330" y="%d" font-size="10" fill="#1F2937">pips left→right: mirrormark · honest · legal · manifest · firewall</text>`+"\n", pipY)

	// Border + halo key.
	pipY2 := y + 44
	fmt.Fprintf(sb, `  <text x="40" y="%d" fill="#1F2937" font-size="11" font-weight="600">Border:</text>`+"\n", pipY2)
	fmt.Fprintf(sb, `  <circle cx="100" cy="%d" r="6" fill="#0F766E" stroke="#10B981" stroke-width="3"/>`+"\n", pipY2-4)
	fmt.Fprintf(sb, `  <text x="112" y="%d" font-size="10" fill="#1F2937">thick = load-bearing wire-in (production code calls Sign)</text>`+"\n", pipY2)
	fmt.Fprintf(sb, `  <text x="40" y="%d" fill="#1F2937" font-size="11" font-weight="600">Halo:</text>`+"\n", pipY2+18)
	fmt.Fprintf(sb, `  <circle cx="100" cy="%d" r="6" fill="#0F766E"/>`+"\n", pipY2+14)
	fmt.Fprintf(sb, `  <circle cx="100" cy="%d" r="9" fill="none" stroke="#F59E0B" stroke-width="2" stroke-dasharray="3 3"/>`+"\n", pipY2+14)
	fmt.Fprintf(sb, `  <text x="112" y="%d" font-size="10" fill="#1F2937">dashed amber = KAT-1 hex pinned</text>`+"\n", pipY2+18)
}

func footer(sb *strings.Builder, o Options) {
	fmt.Fprintf(sb, `  <rect x="0" y="%d" width="%d" height="30" fill="#111827"/>`+"\n", o.Height-30, o.Width)
	if o.Provenance != "" {
		fmt.Fprintf(sb, `  <text x="40" y="%d" fill="#D1D5DB" font-size="11">%s</text>`+"\n",
			o.Height-10, xmlEscape(o.Provenance))
	}
	fmt.Fprintf(sb, `  <text x="%d" y="%d" fill="#D1D5DB" font-size="11" text-anchor="end">KAT-1 %s</text>`+"\n",
		o.Width-40, o.Height-10, scanner.KAT1HexCanonical[:16]+"…")
}

func closeSVG(sb *strings.Builder) {
	fmt.Fprintf(sb, `</svg>`+"\n")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
