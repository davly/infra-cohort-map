# infra-cohort-map

Pure-stdlib Go tool that scans the Limitless **infrastructure layer**
(`infrastructure/*`, `engines/*`, `foundation/aicore/*`) and renders a
visual cohort map to SVG — or emits the same data as a deterministic
YAML / JSON snapshot. Sibling tool to `tools/cohort-map` (which covers
the flagship layer).

## Why a separate map?

The flagship map shows ~319 consumer flagships across ~38 substrate
languages. The infra map shows the ~36 producers that those flagships
*depend on* — engines, services, and foundation libraries. The two
maps answer different questions:

| Question                                  | Map           |
|-------------------------------------------|---------------|
| Which flagships ship at R174 5-of-5?      | cohort-map    |
| Which infra services produce Mirror-Mark? | infra-cohort-map |
| Which engine has the most flagship dependents? | infra-cohort-map |

## CLI

Three sub-commands plus `help` / `--version`:

| Sub-command | Purpose                                              |
|-------------|------------------------------------------------------|
| `render`    | produce an SVG (default), YAML, or JSON snapshot     |
| `scan`      | emit a YAML or JSON snapshot only (no SVG)           |
| `list`      | print consumer-flagship counts per infra to stdout   |

```
infra-cohort-map render --out=infra_map.svg
infra-cohort-map render --out=infra_pre.svg  --projection=current
infra-cohort-map render --out=infra_post.svg --projection=projected
infra-cohort-map render --out=- --format=yaml > snapshot.yaml
infra-cohort-map scan   --format=json --out=snapshot.json
infra-cohort-map scan   --format=yaml --no-scanned-at      # deterministic
infra-cohort-map list
infra-cohort-map --version
```

### Flags

`render` accepts:

| Flag                       | Default                                | Meaning |
|----------------------------|----------------------------------------|---------|
| `--out=<file>`             | `infra_map.svg`                        | output path (`-` = stdout) |
| `--format=svg\|yaml\|json` | `svg`                                  | output format |
| `--projection=current\|projected` | `current`                       | `projected` applies the I3-I20 uplift wave (all 5-of-5) + stamps the banner |
| `--width=<int>`            | `1800`                                 | SVG width px (must be `> 0`, `<= 100000`) |
| `--height=<int>`           | `1100`                                 | SVG height px (must be `> 0`, `<= 100000`) |
| `--title=<str>`            | `Limitless Infrastructure Cohort Map`  | title bar |
| `--subtitle=<str>`         | *(empty)*                              | subtitle text |
| `--snapshot-date=<date>`   | today (UTC)                            | date stamp shown in the title bar |
| `--no-scanned-at`          | `false`                                | omit `generated_at` so YAML/JSON is byte-stable |

`scan` accepts `--out` (default `-`), `--format=yaml|json` (default
`yaml`), and `--no-scanned-at`. `list` takes no format/output flags.

All three commands share the scan-root overrides (defaults point at the
canonical `C:/limitless` layout):

| Flag                  | Default                          |
|-----------------------|----------------------------------|
| `--infra-dir=<path>`  | `C:/limitless/infrastructure`    |
| `--engines-dir=<path>`| `C:/limitless/engines`           |
| `--foundation-dir=<p>`| `C:/limitless/foundation/aicore` |
| `--flagships-dir=<p>` | `C:/limitless/flagships`         |
| `--checkout-root=<p>` | `C:/limitless`                   |

`--checkout-root` is the prefix that component `path` fields are emitted
relative to (with forward slashes), so a YAML/JSON snapshot is identical
across operator machines. Combined with `--no-scanned-at`, snapshot
output is byte-for-byte reproducible (covered by the golden tests).

### Exit codes

Stable across versions — regulator-side automation may branch on them
(mirrors `cohort-map` / `lore-mark-verify`):

| Code | Meaning |
|------|---------|
| `0`  | OK |
| `6`  | usage error (bad sub-command / flag / dimension / format) |
| `7`  | I/O error (scan, consumer-count, or write failed) |
| `8`  | render error (YAML/JSON marshal failed) |

## Detection rules

For each `infrastructure/<infra>`, `engines/<engine>`, and
`foundation/aicore` root:

1. **Substrate**: from `go.mod` / `pyproject.toml` / `Cargo.toml` /
   `shard.yml` / `package.json`.
2. **R174 cohort packages**: check for presence of all five —
   `mirrormark`, `honest`, `legal`, `manifest`, `firewall` — under
   `internal/` or `pkg/`.
3. **Load-bearing wire-in**: non-test code calls `mirrormark.Sign(`,
   `marker.Sign(`, or `MirrorMark.Sign(`.
4. **KAT-1 byte-identity**: any file under the infra root pins the
   canonical KAT-1 hex `239a7d0d3f1bbe3a98aede01e2ad818c2db60b7177c02e2f015035b2b5b7dbca`.
5. **Consumer flagships**: count of `flagships/<f>/internal/<infra>`
   directories (or `flagships/<f>/<dialect>/<infra>` variants).

## SVG output

- Nodes color-coded by substrate (Go = teal, Python = blue, etc.).
- Node radius scales with consumer-flagship count.
- 5-pip indicator for the R174 cohort: green pip = package present,
  gray pip = absent.
- Thicker border = load-bearing wire-in (production code calls Sign).
- Edges drawn when one infra has an `internal/<other-infra>` subdir.

## Snapshot schema (YAML / JSON)

`scan` and `render --format=yaml|json` emit `schema_version: 1`. Top
level: `schema_version`, `generated_at` (empty when `--no-scanned-at`),
`kat1_canonical_hex`, and a `components` list sorted by `(layer, name)`.
Each component:

| Field             | Type           | Notes |
|-------------------|----------------|-------|
| `name`            | string         | directory name |
| `layer`           | string         | `infrastructure` \| `engine` \| `foundation` |
| `path`            | string         | checkout-relative, forward slashes |
| `substrate`       | string         | `go`, `python`, `rust`, … or `unknown` |
| `go_module`       | string         | module path (omitted if not Go) |
| `cohort_count`    | int            | 0–5 R174 packages present |
| `package_status`  | map/struct     | per-package booleans (`mirrormark`…`firewall`) |
| `load_bearing`    | bool           | production code calls `Sign(` |
| `kat1_pinned`     | bool           | KAT-1 hex found under the root |
| `consumer_count`  | int            | number of consuming flagships |
| `consumers`       | []string       | sorted flagship names (omitted if none) |
| `internal_deps`   | []string       | cross-infra `internal/<other>` deps (omitted if none) |
| `notes`           | []string       | scanner annotations (omitted if none) |

The YAML form is a strict, hand-rolled subset (no `gopkg.in/yaml.v3`
dependency); JSON is `encoding/json` `MarshalIndent`. Both sort map keys
and consumer lists so the same filesystem produces byte-identical bytes.

## Shipped snapshots

- `infra_pre_uplift.svg` — baseline before the I3-I20 uplift wave.
- `infra_post_uplift.svg` — projected after I3-I20 lands
  (`render --projection=projected`).

Both ship alongside `manifests/infra_cohort_2026-05-28.yaml`.
