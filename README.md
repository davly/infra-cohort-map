# infra-cohort-map

Pure-stdlib Go tool that scans the Limitless **infrastructure layer**
(`infrastructure/*`, `engines/*`, `foundation/aicore/*`) and renders a
visual cohort map to SVG. Sibling tool to `tools/cohort-map` (which
covers the flagship layer).

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

    infra-cohort-map render --out=infra_map.svg
    infra-cohort-map render --out=infra_pre.svg  --projection=current
    infra-cohort-map render --out=infra_post.svg --projection=projected

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

## Output

- Nodes color-coded by substrate (Go = teal, Python = blue, etc.).
- Node radius scales with consumer-flagship count.
- 5-pip indicator for the R174 cohort: green pip = package present,
  gray pip = absent.
- Thicker border = load-bearing wire-in (production code calls Sign).
- Edges drawn when one infra has an `internal/<other-infra>` subdir.

## Snapshots

- `infra_pre_uplift.svg` — baseline before the I3-I20 uplift wave.
- `infra_post_uplift.svg` — projected after I3-I20 lands.

Both ship alongside `manifests/infra_cohort_2026-05-28.yaml`.
