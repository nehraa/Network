# Mixnet local benchmark runner

This benchmark suite runs entirely on local libp2p hosts. It does not use Docker.

## What it measures

- Direct libp2p baseline over Noise.
- Header-only onion and full onion transport latency.
- CES pipeline cost by data size.
- Hop-count sweep.
- Parallel-circuit sweep.
- Hop x circuit efficiency search.
- Erasure-threshold sweep.
- Header padding, payload padding, jitter, and auth-tag overhead.
- Compression mode comparisons.
- Relay selection mode comparisons.
- Key-exchange cost as a separate timing column in the summary output.

## Raw data and outlier rule

Each scenario and data size is run multiple times.

- Full profile: 6 runs.
- Smoke profile: 3 runs.
- Raw files keep every run.
- The aggregator excludes the single run farthest from the median total latency for that scenario and size.
- Means and sample standard deviations are computed from the remaining runs.

## Default size sweep

The `full` profile uses:

- `1KB,4KB,16KB,64KB,256KB,1MB,4MB,16MB,32MB,50MB`

## Run it

Full sweep:

```bash
./mixnet/run_local_benchmarks.sh
```

Quick smoke validation:

```bash
MIXNET_BENCH_PROFILE=smoke ./mixnet/run_local_benchmarks.sh
```

Targeted run:

```bash
./mixnet/run_local_benchmarks.sh \
  --groups mode-overview,ces-pipeline \
  --sizes 1KB,64KB,1MB \
  --runs 6
```

## Output

Every run writes a timestamped directory under `mixnet/benchmarks/output/` with:

- `raw_runs.csv`
- `raw_runs.jsonl`
- `summary.csv`
- `summary.json`
- `best_hops_circuits.csv`
- `best_hops_circuits.json`
- `metadata.json`
- `report.html`
- `graphs/*.svg`

`report.html` links the generated graphs and summarizes the best hop x circuit combinations per size.

## Notes

- The payload generator uses deterministic random bytes so compression numbers reflect a noise-like payload, not highly compressible text.
- CES reconstruction measurements use only the Reed-Solomon threshold subset, not all shards, so the reported pipeline timings reflect early reconstruction behavior.
