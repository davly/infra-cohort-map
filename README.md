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

Three sub-commands plus the meta flags `-h`/`--help` and
`-version`/`--version`/`-v`:

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
infra-cohort-map scan   --require-5of5 --out=- >/dev/null  # CI exit gate
infra-cohort-map list
infra-cohort-map -version                                  # JSON contract
```

### Version

`-version` (or `--version` / `-v`) prints a single line of machine-readable
JSON to stdout and exits `0`, carrying the binary version and the snapshot
schema version a caller can pin against:

```
$ infra-cohort-map -version
{"tool":"infra-cohort-map","version":"v0.1.0","schema_version":1}
```

`schema_version` is the version of the YAML/JSON snapshot contract (see
[Snapshot schema](#snapshot-schema-yaml--json)); it is the same value stamped
into every emitted snapshot, so the reported and emitted schema versions can
never drift.

### Flags

Machine-readable output is selected with `--format` (`svg`/`yaml`/`json`),
**not** a `--json` boolean — unlike the sibling `tools/cohort-map`, which
exposes a `--json` flag on its `verify`/`check` sub-commands. Here, pass
`--format=json` to `render` or `scan`.

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
`yaml`), `--no-scanned-at`, and two optional exit-gate flags:

| Flag                    | Meaning |
|-------------------------|---------|
| `--require-5of5`        | exit `9` unless **every** scanned component is 5-of-5 (`cohort_count == 5`) |
| `--require-loadbearing` | exit `9` unless **every** scanned component is load-bearing |

The gates make `scan` usable as a CI guard. The snapshot is still emitted
(to `--out`) *before* the gate is checked, so a failing run leaves a full
report behind; a per-component diagnostic naming each sub-bar component is
written to **stderr only** (stdout stays byte-identical with or without a
gate). Both gates may be combined (a component must clear both). They are
**fail-closed**: if a gate is requested but the scan found zero components
(e.g. a mis-pointed root) the run exits `9` rather than vacuously passing.
`list` takes no format/output flags.

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
| `9`  | exit-gate unmet (`scan --require-5of5` / `--require-loadbearing`) |

## Detection rules

For each `infrastructure/<infra>`, `engines/<engine>`, and
`foundation/aicore` root:

1. **Substrate**: from `go.mod` / `pyproject.toml` / `Cargo.toml` /
   `shard.yml` / `package.json`.
2. **R174 cohort packages**: check for presence of all five —
   `mirrormark`, `honest`, `legal`, `manifest`, `firewall` — under
   `internal/` or `pkg/`. Presence requires a non-test `.go` file:
   empty placeholder dirs and test-only dirs count **absent**, but a
   dir holding only `_test.go` files (echo's pure-test firewall
   pattern) is surfaced as a `<pkg>: test-only` note so a 4-of-5 is
   explainable.
3. **Load-bearing wire-in**: non-test code calls `mirrormark.Sign(`,
   `marker.Sign(`, or `MirrorMark.Sign(`.
4. **KAT-1 byte-identity**: any file under the infra root pins the
   canonical KAT-1 hex `239a7d0d3f1bbe3a98aede01e2ad818c2db60b7177c02e2f015035b2b5b7dbca`.
5. **Consumer flagships** (`consumer_count` / `consumers`): count of
   `flagships/<f>/internal/<infra>` directories (or
   `flagships/<f>/<dialect>/<infra>` variants). The hit directory must
   contain at least one non-test source file (any substrate, anywhere
   under it) — empty placeholder dirs and dirs holding only
   docs/fixtures/test files do **not** count, mirroring the
   cohort-package placeholder guard in rule 2.
6. **Module consumers** (`module_consumer_count` / `module_consumers`):
   for every component whose `go_module` is known, the set of repos —
   across **all four** layer trees (`infrastructure/`, `engines/`,
   `foundation/aicore/`, `flagships/`) — whose `go.mod` actually depends
   on that module. A repo counts when its `go.mod` has a **`require`**
   directive for the module path, **or** a **`replace`** directive whose
   target local path resolves to the component's own directory (both
   forms count; no suffix is added — the value is a clean component/dir
   name). The component's own module is excluded (no self-edges); `vendor/`
   and `testdata/` subtrees are skipped. A `go.mod` nested under a
   component (the `nexus/src/api/go.mod` pattern) is attributed to the
   top-level component (`nexus`).

### `consumers` (SoftProxy) vs `module_consumers` (receipt)

These two fields answer **different questions** and must not be conflated:

| Field | Trust tier | Detects | Answers |
|-------|-----------|---------|---------|
| `consumers` / `consumer_count` | **SoftProxy** — R174 cohort-*pattern* adoption | a same-named `<anchor>/<infra>/` directory with source in it | "which flagships host this component's cohort pattern?" |
| `module_consumers` / `module_consumer_count` | **Receipt** — a `go.mod`-parsed build fact | a real `require`/local-`replace` of the module | "which repos actually build against this module?" |

The census is a *proxy*: it counts vendored pattern replicas (which often
carry the component's **name** but import none of its code) and — because
high-degree libraries are consumed by module path, not by copying a
same-named dir — it can **invert real dependency degree**. On the live
estate `aicore` has a directory census of `0` but a **module degree of
36** (required by 28 infrastructure + 5 engine + 3 flagship modules), while
`lore` shows a census of `53` but a **module degree of `1`** (only
`nexus/src/api` actually requires it). Read `module_consumers` for
blast-radius / deprecation decisions; read `consumers` only as a
pattern-adoption signal.

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
| `consumer_count`  | int            | **SoftProxy** — number of consuming flagships by cohort-pattern dir census |
| `consumers`       | []string       | sorted flagship names (omitted if none) |
| `module_consumer_count` | int      | **receipt** — number of repos whose `go.mod` requires/local-replaces this module (present whenever `go_module` is known, even when `0`; omitted for non-Go components) |
| `module_consumers`| []string       | sorted consumer component/dir names from the `go.mod` parse (omitted if none) |
| `internal_deps`   | []string       | cross-infra `internal/<other>` deps (omitted if none); the component's own name is excluded — an `internal/<own-name>` local-types package (the delve pattern) is not a dependency edge |
| `notes`           | []string       | scanner annotations (omitted if none) |

`module_consumer_count` / `module_consumers` are **additive** — they do not
bump `schema_version` (house convention: additive fields keep the contract
number stable). Both are sorted, so the same filesystem yields byte-identical
output.

The YAML form is a strict, hand-rolled subset (no `gopkg.in/yaml.v3`
dependency); JSON is `encoding/json` `MarshalIndent`. Both sort map keys
and consumer lists so the same filesystem produces byte-identical bytes.

## Shipped snapshots

- `infra_pre_uplift.svg` — baseline before the I3-I20 uplift wave.
- `infra_post_uplift.svg` — projected after I3-I20 lands
  (`render --projection=projected`).

Both ship alongside `manifests/infra_cohort_2026-05-28.yaml`.
