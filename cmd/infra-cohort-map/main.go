// Command infra-cohort-map walks the Limitless infrastructure layer
// (infrastructure/ + engines/ + foundation/aicore/) and renders a
// visual cohort map to SVG.  Sibling tool to cohort-map (which covers
// the flagship layer).
//
// Usage:
//
//	infra-cohort-map render --out=infra_map.svg
//	infra-cohort-map render --out=infra_pre.svg  --projection=current
//	infra-cohort-map render --out=infra_post.svg --projection=projected
//	infra-cohort-map render --out=- --format=yaml > snapshot.yaml
//	infra-cohort-map scan   --format=yaml --out=snapshot.yaml
//	infra-cohort-map list
//
// Exit codes mirror cohort-map / lore-mark-verify (zero-based, stable
// across versions, regulator-side automation may branch on these).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davly/infra-cohort-map/internal/relate"
	"github.com/davly/infra-cohort-map/internal/render"
	"github.com/davly/infra-cohort-map/internal/scanner"
)

const version = "v0.1.0"

const (
	exitOK     = 0
	exitUsage  = 6
	exitIO     = 7
	exitRender = 8
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "render":
		return cmdRender(args[1:], stdout, stderr)
	case "scan":
		return cmdScan(args[1:], stdout, stderr)
	case "list":
		return cmdList(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		printUsage(stdout)
		return exitOK
	case "--version":
		fmt.Fprintln(stdout, version)
		return exitOK
	default:
		fmt.Fprintf(stderr, "infra-cohort-map: unknown sub-command %q\n\n", args[0])
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "infra-cohort-map "+version+" — render Limitless infrastructure cohort map")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Sub-commands:")
	fmt.Fprintln(w, "  render   produce SVG (default) or YAML snapshot")
	fmt.Fprintln(w, "  scan     emit YAML snapshot only (no render)")
	fmt.Fprintln(w, "  list     print consumer counts per infra to stdout")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Common flags (render):")
	fmt.Fprintln(w, "  --out=<file>          output path ('-' for stdout)")
	fmt.Fprintln(w, "  --infra-dir=<path>    override infrastructure dir")
	fmt.Fprintln(w, "  --engines-dir=<path>  override engines dir")
	fmt.Fprintln(w, "  --foundation-dir=<p>  override foundation/aicore dir")
	fmt.Fprintln(w, "  --flagships-dir=<p>   override flagships dir")
	fmt.Fprintln(w, "  --projection=current|projected   banner stamp + projected uplift")
	fmt.Fprintln(w, "  --width=<int>  --height=<int>     SVG size (must be > 0)")
	fmt.Fprintln(w, "  --title=<str>  --subtitle=<str>")
	fmt.Fprintln(w, "  --checkout-root=<path>  emit component paths relative to this root")
	fmt.Fprintln(w, "  --no-scanned-at         omit generated_at (deterministic YAML/JSON)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Exit codes:")
	fmt.Fprintln(w, "  0 OK · 6 usage · 7 I/O · 8 render error")
}

type commonFlags struct {
	infraDir      string
	enginesDir    string
	foundationDir string
	flagshipsDir  string
	checkoutRoot  string
}

func registerCommon(fs *flag.FlagSet) *commonFlags {
	d := scanner.DefaultRoots()
	c := &commonFlags{}
	fs.StringVar(&c.infraDir, "infra-dir", d.InfrastructureDir, "path to infrastructure dir")
	fs.StringVar(&c.enginesDir, "engines-dir", d.EnginesDir, "path to engines dir")
	fs.StringVar(&c.foundationDir, "foundation-dir", d.FoundationDir, "path to foundation/aicore dir")
	fs.StringVar(&c.flagshipsDir, "flagships-dir", relate.DefaultFlagshipsDir(), "path to flagships dir")
	fs.StringVar(&c.checkoutRoot, "checkout-root", defaultCheckoutRoot(), "checkout root paths are emitted relative to (machine-independent YAML/JSON)")
	return c
}

// defaultCheckoutRoot is the common ancestor of the default scan roots
// (infrastructure / engines / foundation all live under it). Emitting
// component paths relative to it keeps the snapshot reproducible across
// operator machines.
func defaultCheckoutRoot() string {
	return filepath.FromSlash("C:/limitless")
}

// relativizePath returns p expressed relative to root using forward
// slashes. If root is empty, or p escapes root (Rel yields a "..")
// path, the absolute slash-path is returned unchanged so nothing is
// silently mangled.
func relativizePath(root, p string) string {
	if root == "" {
		return filepath.ToSlash(p)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(rel)
}

func (c *commonFlags) Roots() scanner.Roots {
	return scanner.Roots{
		InfrastructureDir: c.infraDir,
		EnginesDir:        c.enginesDir,
		FoundationDir:     c.foundationDir,
	}
}

func cmdRender(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := registerCommon(fs)
	outPath := fs.String("out", "infra_map.svg", "path to write SVG (- for stdout)")
	format := fs.String("format", "svg", "svg | yaml")
	width := fs.Int("width", 1800, "SVG width in px")
	height := fs.Int("height", 1100, "SVG height in px")
	title := fs.String("title", "Limitless Infrastructure Cohort Map", "title bar")
	subtitle := fs.String("subtitle", "", "subtitle text")
	snapshot := fs.String("snapshot-date", time.Now().UTC().Format("2006-01-02"), "snapshot date stamp")
	projection := fs.String("projection", "current", "current | projected")
	noScannedAt := fs.Bool("no-scanned-at", false, "omit the generated_at timestamp (deterministic output)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	const maxDim = 100000
	if *width <= 0 || *height <= 0 {
		fmt.Fprintln(stderr, "render: --width and --height must be positive")
		return exitUsage
	}
	if *width > maxDim || *height > maxDim {
		fmt.Fprintf(stderr, "render: --width and --height must be <= %d\n", maxDim)
		return exitUsage
	}

	snap, err := scanner.ScanAll(common.Roots())
	if err != nil {
		fmt.Fprintln(stderr, "render: scan:", err)
		return exitIO
	}
	if !*noScannedAt {
		snap.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// Feed the live scan's component names into the consumer counter so
	// the search set is the infra that actually exists on disk — this
	// removes the drift risk between scanner.knownInfraNames() and the
	// relate package's fallback list.
	consumers, err := relate.CountConsumers(common.flagshipsDir, componentNames(snap))
	if err != nil {
		fmt.Fprintln(stderr, "render: relate:", err)
		return exitIO
	}

	if strings.EqualFold(*projection, "projected") {
		snap = applyProjection(snap)
	}

	switch strings.ToLower(*format) {
	case "svg":
		opts := render.Options{
			Width:      *width,
			Height:     *height,
			Title:      *title,
			Subtitle:   *subtitle,
			Snapshot:   *snapshot,
			Projection: strings.ToLower(*projection),
			Provenance: fmt.Sprintf("infra-cohort-map %s · scanned %d components · flagships %d",
				version, len(snap.Components), countAllConsumers(consumers)),
		}
		out := render.Render(snap, consumers, opts)
		return writeOut(*outPath, out, stdout, stderr)
	case "yaml":
		out := []byte(toYAML(snap, consumers, common.checkoutRoot))
		return writeOut(*outPath, out, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "render: --format must be svg|yaml")
		return exitUsage
	}
}

func cmdScan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := registerCommon(fs)
	outPath := fs.String("out", "-", "path to write YAML (- for stdout)")
	format := fs.String("format", "yaml", "yaml")
	noScannedAt := fs.Bool("no-scanned-at", false, "omit the generated_at timestamp (deterministic output)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.ToLower(*format) != "yaml" {
		fmt.Fprintln(stderr, "scan: only --format=yaml is supported")
		return exitUsage
	}
	snap, err := scanner.ScanAll(common.Roots())
	if err != nil {
		fmt.Fprintln(stderr, "scan:", err)
		return exitIO
	}
	if !*noScannedAt {
		snap.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}
	consumers, err := relate.CountConsumers(common.flagshipsDir, componentNames(snap))
	if err != nil {
		// Consumer count is decoration — emit snapshot without it, but
		// surface the reason rather than silently reporting zeroes.
		fmt.Fprintln(stderr, "scan: consumer count unavailable:", err)
		consumers = relate.Consumers{}
	}
	out := []byte(toYAML(snap, consumers, common.checkoutRoot))
	return writeOut(*outPath, out, stdout, stderr)
}

func cmdList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	common := registerCommon(fs)
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	// Scan first so the consumer counter searches for the infra that
	// actually exists on disk (shared source of truth with render/scan).
	snap, err := scanner.ScanAll(common.Roots())
	if err != nil {
		fmt.Fprintln(stderr, "list: scan:", err)
		return exitIO
	}
	consumers, err := relate.CountConsumers(common.flagshipsDir, componentNames(snap))
	if err != nil {
		fmt.Fprintln(stderr, "list:", err)
		return exitIO
	}
	for _, p := range consumers.Pairs() {
		fmt.Fprintf(stdout, "%4d  %s\n", p.Count, p.Name)
	}
	return exitOK
}

func writeOut(path string, data []byte, stdout, stderr io.Writer) int {
	if path == "-" {
		_, err := stdout.Write(data)
		if err != nil {
			fmt.Fprintln(stderr, "write stdout:", err)
			return exitIO
		}
		return exitOK
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(stderr, "write mkdir:", err)
		return exitIO
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(stderr, "write:", err)
		return exitIO
	}
	fmt.Fprintf(stdout, "wrote %s (%d bytes)\n", path, len(data))
	return exitOK
}

// componentNames returns the names of the scanned components. Passing
// these into relate.CountConsumers makes the live scan — not a hardcoded
// fallback list — the authority on which infra to count consumers for.
// An empty result (e.g. infra dirs absent on a CI box) lets
// CountConsumers fall back to its built-in default list.
func componentNames(snap scanner.Snapshot) []string {
	names := make([]string, 0, len(snap.Components))
	for _, c := range snap.Components {
		names = append(names, c.Name)
	}
	return names
}

func countAllConsumers(c relate.Consumers) int {
	seen := map[string]bool{}
	for _, fs := range c {
		for _, f := range fs {
			seen[f] = true
		}
	}
	return len(seen)
}

// applyProjection returns a copy of snap with the I3-I20 uplift wave
// applied. Every infrastructure / engine component is forced to a
// 5-of-5 cohort + KAT-1 pin + load-bearing wire-in.  The current state
// (mostly empty placeholder dirs) becomes the green-pip cohort.
func applyProjection(snap scanner.Snapshot) scanner.Snapshot {
	out := scanner.Snapshot{GeneratedAt: snap.GeneratedAt}
	for _, c := range snap.Components {
		c.PackageStatus = [5]bool{true, true, true, true, true}
		c.CohortCount = 5
		c.LoadBearing = true
		c.KAT1Pinned = true
		c.Notes = []string{"projected — I3-I20 uplift wave complete"}
		out.Components = append(out.Components, c)
	}
	return out
}

// toYAML emits a deterministic YAML representation of the snapshot
// (pure stdlib — no gopkg.in/yaml.v3 dependency). Format is a strict
// subset of YAML so external tooling can pin to it.
func toYAML(snap scanner.Snapshot, cons relate.Consumers, pathRoot string) string {
	var sb strings.Builder
	sb.WriteString("# infra-cohort-map snapshot — auto-generated\n")
	sb.WriteString("# Do not edit by hand; regenerate with `infra-cohort-map scan`.\n")
	sb.WriteString("schema_version: 1\n")
	if snap.GeneratedAt == "" {
		sb.WriteString("generated_at:\n")
	} else {
		sb.WriteString("generated_at: " + snap.GeneratedAt + "\n")
	}
	sb.WriteString("kat1_canonical_hex: " + scanner.KAT1HexCanonical + "\n")
	sb.WriteString("components:\n")
	for _, c := range snap.Components {
		sb.WriteString("  - name: " + yamlStr(c.Name) + "\n")
		sb.WriteString("    layer: " + yamlStr(string(c.Layer)) + "\n")
		sb.WriteString("    path: " + yamlStr(relativizePath(pathRoot, c.Path)) + "\n")
		sb.WriteString("    substrate: " + yamlStr(c.Substrate) + "\n")
		if c.GoModule != "" {
			sb.WriteString("    go_module: " + yamlStr(c.GoModule) + "\n")
		}
		sb.WriteString("    cohort_count: " + fmt.Sprintf("%d", c.CohortCount) + "\n")
		sb.WriteString("    package_status:\n")
		for i, name := range scanner.CohortPackages {
			sb.WriteString("      " + name + ": " + fmt.Sprintf("%t", c.PackageStatus[i]) + "\n")
		}
		sb.WriteString("    load_bearing: " + fmt.Sprintf("%t", c.LoadBearing) + "\n")
		sb.WriteString("    kat1_pinned: " + fmt.Sprintf("%t", c.KAT1Pinned) + "\n")
		sb.WriteString("    consumer_count: " + fmt.Sprintf("%d", len(cons[c.Name])) + "\n")
		if len(cons[c.Name]) > 0 {
			cs := append([]string(nil), cons[c.Name]...)
			sort.Strings(cs)
			sb.WriteString("    consumers:\n")
			for _, f := range cs {
				sb.WriteString("      - " + yamlStr(f) + "\n")
			}
		}
		if len(c.InternalDeps) > 0 {
			sb.WriteString("    internal_deps:\n")
			for _, d := range c.InternalDeps {
				sb.WriteString("      - " + yamlStr(d) + "\n")
			}
		}
		if len(c.Notes) > 0 {
			sb.WriteString("    notes:\n")
			for _, n := range c.Notes {
				sb.WriteString("      - " + yamlStr(n) + "\n")
			}
		}
	}
	return sb.String()
}

func yamlStr(s string) string {
	// Conservative quoting: always quote so future edits to the value
	// don't accidentally turn into bools / numerics / null.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
