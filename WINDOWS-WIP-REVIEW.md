# Windows WIP integration review — 2026-10-16

The owner authorized repairing and integrating archived commit `d39db40` (`archive/windows-wip-2026-10-16`). The archive remains preserved. This supersedes the earlier blocked review; it does not establish native Windows qualification.

| Defect | Correction | Evidence |
|---|---|---|
| GGUF saved unsupported `llama` runtime | Retained header-based candidate detection and existing OpenAI recipe mapping | Serving/TUI regressions |
| Rulith used MLX load path | Explicit manager catalog/start/stop with matching status and HTTP model confirmation | Fake-manager subprocess and HTTP fixtures |
| Windows process cleanup regressed to leader-only kill | Preserved suspended Job Object process-tree implementation | Windows compile and existing platform tests |
| Bundle omitted templates and native attention module; BOM broke JSON | Included assets, UTF-8 without BOM, incomplete-build refusal; offline builder remains default | Manifest fixtures, bundle tests, Python syntax checks |
| Remote/local endpoints matched by port | Compare scheme, host, effective port and API prefix | Endpoint identity tests |
| Discovery dropped multiple/IPv6 listener addresses and ignored cancellation | Preserve each address/port, propagate caller context, remove shell command-line reconstruction | Mac parser/cancellation fixtures; cross-compilation |
| Arbitrary HTTP errors misidentified services | Require positive protocol health for unconfigured discovery | Fake transport regressions |
| Dormant external PID kill/relaunch helpers unsafe | Removed unused helpers; discovery is read-only | Source review |
| Windows terminal arguments double-quoted | Preserve argv, separate command boundary, refuse Windows Terminal separators | Pure argument tests on Mac |

Native decision models remain the only judge route. Existing PAT, tool recovery and native D1 reliability changes are preserved. No Laguna run was restarted. Windows/Linux execution, PowerShell packaging, Rulith installation compatibility and target GPU inference still require native qualification; fixtures and cross-compilation do not substitute for it.

Final validation and published revisions are recorded in ConstructionPlan.md.
