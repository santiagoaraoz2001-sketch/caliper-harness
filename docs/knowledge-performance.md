# Knowledge retrieval cost — v0.2.2 local candidate

Measured 2026-10-24 on the HP, Windows amd64, AMD Ryzen AI MAX+ PRO 395. This is a local Kuzu/GraphQL microbenchmark, not evidence of improved model reasoning or end-to-end game completion.

## Method

`BenchmarkKnowledgeCost` creates temporary graphs with 100, 1,000 and 10,000 current project notes, each about 800 bytes. It additionally creates a target with 32 sections, one document referencing all 32 sections, and one document attached to a roughly 4 KiB source file. Each case executes 20 bounded GraphQL requests against the same open database. Setup is outside measured sub-benchmarks. No inference, network, persistent cache or full-directory scan is involved.

The before/after runs are single local samples. Other development work can affect timings; these are mean request times, not p95 or cold-start results. Historical-version accumulation, multiple concurrent readers, very large documents and HDD performance are not characterized here.

The initial before/after timings preceded the later Windows junction-containment correction, which resolves file handles through the OS instead of relying solely on Go symlink resolution. A final-candidate rerun including that correction is recorded below.

## Demonstrated waste and change

Previously, `references { key section }` looked up every target even though no status was requested. Multiple edges to different sections of one target also reread the same document once per edge. With 32 edges this meant one edge query plus 32 target queries, even for metadata-only output.

The resolver now detects whether `status` is selected, including aliases and fragments. Metadata-only output runs the edge query without resolving targets. When status is requested, a local map within that single call reuses each target read; multiple section links to the same target now need one target query. Missing targets are also reused within that call. Nothing is retained across requests, so later edits remain visible. Source bytes are charged once per actual read; output rows/bytes and missing-section checks remain enforced.

No index, background refresh, persistent cache, extra tool or dependency was introduced. Freshness already reads only the explicitly attached file when selected; its implementation was retained.

## Results

Mean milliseconds per request, before → after:

| Current notes | Exact key + body | Keyword + top 3 sections | 32 reference keys/sections | Same references + status | File freshness |
|---:|---:|---:|---:|---:|---:|
| 100 | 16.94 → 16.97 | 8.57 → 10.45 | 247.18 → 8.63 | 253.94 → 17.13 | 11.32 → 9.87 |
| 1,000 | 7.62 → 7.92 | 12.36 → 12.35 | 259.75 → 8.74 | 257.26 → 15.82 | 11.33 → 9.39 |
| 10,000 | 9.38 → 10.69 | 44.89 → 47.26 | 326.01 → 12.31 | 313.39 → 23.40 | 12.32 → 14.08 |

Serialized result sizes stayed unchanged: exact key 864 bytes; keyword results 2,628 bytes; reference metadata 1,567 bytes; references with status 2,207 bytes; freshness 50 bytes. These are bytes, not tokenizer-dependent token counts. The improvement reduces host work without expanding model context.

Keyword lookup still scans matching project content; output limits do not establish constant database work. At 10,000 current notes it measured about 47 ms here. There is no measured reason yet to add another search index. Exact keys and needed sections remain the preferred continuation route after discovery.

## Final candidate after Windows junction correction

The same benchmark passed again with the final path-resolution helper, 20 operations per case. During this run, the broader Go run-fixture package was active and the Rulith model was loaded but idle after a READY-only smoke. The Space Transporter challenge had not started. These conditions differ from the earlier samples, so the numbers are observations rather than a controlled claim about the junction correction's performance impact.

Mean milliseconds per request:

| Current notes | Exact key + body | Keyword + top 3 sections | 32 reference keys/sections | Same references + status | File freshness |
|---:|---:|---:|---:|---:|---:|
| 100 | 18.67 | 11.08 | 9.63 | 21.33 | 10.59 |
| 1,000 | 9.38 | 11.98 | 9.38 | 17.46 | 9.87 |
| 10,000 | 10.19 | 58.60 | 11.08 | 21.18 | 10.38 |

Result sizes remain identical to the earlier runs: 864, 2,628, 1,567, 2,207 and 50 bytes respectively. The regression-tested reduction in target reads is unchanged. The approximately 59 ms keyword search at 10,000 current notes remains worth watching as histories grow; this sample alone does not justify extra indexing infrastructure. Raw output: `%TEMP%/caliper-v022-knowledge-cost-final.log` (package completed in 6.367 seconds).

## Validation and reproduction

Regressions verify that metadata-only references do not consume target bodies, repeated target sections consume the target once, aliases/fragments preserve selection behavior, and requesting status retains the source-byte limit. Existing knowledge navigation, reference statuses, project isolation and freshness tests remain applicable.

The full graph and memory packages passed after the change (22.147 seconds and 1.720 seconds respectively); raw output is `%TEMP%/caliper-v022-knowledge-cost-full.log`.

Run from the source checkout:

```powershell
go test ./internal/graph ./internal/memory -run 'Test(Knowledge|Builtin)' -count=1 -timeout 3m
go test ./internal/graph -run '^$' -bench BenchmarkKnowledgeCost -benchtime=20x -count=1 -timeout 5m
```

Raw logs are in `%TEMP%/caliper-v022-knowledge-cost-before.log`, `caliper-v022-knowledge-cost-after.log` and `caliper-v022-knowledge-cost-regression.log`. The temporary databases are managed by Go's benchmark/test cleanup.
