# Windows WIP review — 2026-10-16

Reviewed commit d39db40 (original `windows` branch). **Not merged:** the owner's merge condition was not met. The entire WIP history is retained by `archive/windows-wip-2026-10-16`.

| Finding | Evidence | Required correction before integration |
|---|---|---|
| Invalid worker configuration | WIP `internal/tui/serve.go` assigns asset runtime `llama`; runtime validation rejects it. | Keep asset format separate from the OpenAI wire runtime; test selection through validation. |
| Load controls dispatch wrong runtime | Rulith/GGUF rows admitted, then MLX preparation/readiness used. RulithStart has no caller. | Implement and test appropriate existing manager/recipe dispatch before claiming loading support. |
| Process cleanup regression | WIP Windows process implementation kills only the leader. | Preserve synchronized branch's suspended Job Object process-tree ownership. |
| Incomplete package | WIP bundle removes configured template path but omits template files. | Include templates and verify relocated inventory. |
| Endpoint identity collision | Discovery compares ports without distinguishing remote/local hosts. | Compare actual endpoint identity; test remote/local equal-port cases. |
| Cancellation gap | Listener inspection starts subprocesses without the discovery context. | Pass cancellation through OS command execution and test settlement. |

Discovery/status/catalog displays are wired. Captured-PID stop and reconstructed-command relaunch helpers are dormant; they need identity/argument validation before exposure. The earlier native-judge restrictions and subsequent reliability fixes remain in the validated Windows branch.

This was a source/merge review, not native Windows validation. The trial merge was aborted; no WIP functionality or partial conflict resolution was published. Validated branches are named MacOS, Windows and Linux. MacOS remains the default. No Laguna run was started.
