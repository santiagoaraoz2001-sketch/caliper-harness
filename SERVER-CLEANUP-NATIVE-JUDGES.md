# Resident cleanup and native-only judges — 2026-10-15

Owner-authorized scope: terminate the process trees of servers launched by the harness; remove the ordinary OpenAI chat judge adapter; validate on the Mac and publish the shared changes to main, Windows and Linux. No production services or model inference are launched for this correction. No new limits or defaults.

## Contracts

- Only owned recipe processes may be stopped. External server endpoints remain external.
- Stop and normal leader exit clean up ordinary descendants. Explicitly detached Unix sessions are outside process-group ownership; this is not a process sandbox.
- Windows children must enter an owned kill-on-close Job before they execute.
- Native decision runtimes remain Laya and System One. Ordinary worker chat models remain supported, but cannot be selected as judges through an OpenAI adapter. Existing openai judge configurations must fail clearly rather than silently change model or protocol.
- Preserve historical trace and adapter-evaluation evidence, clearly marking superseded behavior.

## Verification and publication

Implementation and validation in progress. Evidence directory: verification/2026-10-15-native-judge-cleanup/.
Mac process fixtures test actual child cleanup without loading a model. Windows/Linux cross-builds establish compilation only; native Kuzu/CGO linking, packaging and runtime operation remain unverified without target hosts.

## Implemented and reviewed

- Resident launch establishes a Unix process group or suspended Windows process assigned to a kill-on-close Job. Stop is concurrent/idempotent; natural launcher exit also releases descendants.
- Removed chat-judge transport, its prompt, generation-allowance wiring, Settings option and ordinary catalog selection route. Native-only checks cover configuration, direct client requests and sidecar recipes. Historical session confidence provenance remains readable.
- Existing `decision.runtime: openai` configurations must select Laya/System One. Custom prompt overrides must remove the retired `decision.chat.instructions` entry. Ordinary worker chat models remain supported.
- Independent review found no remaining blocking defect. It corrected an obsolete test runtime label without weakening its request-size assertion.

## Mac component rubric

No model inference in this correction; these are component outcomes, not fresh Snake capability results. All model/runtime/sampler fields are not applicable. Historical traces and installed services were preserved.

| Function | Result | Observation |
|---|---|---|
| F01 Setup/lifecycle | PASS, scoped | Actual child listener closes on Stop and launcher exit; unrelated listener survives. Native-only selections tested. |
| F02 Transport | PASS, scoped | Unsupported judge runtimes refuse before any network request; native client regressions pass. |
| F03 Prompts | PASS, scoped | Removed retired adapter prompt and validated prompt catalog. |
| F04 Discovery | — | No model source-discovery run. |
| F05 Changes | — | No model editing run. |
| F06 Commands | — | Resident lifecycle belongs to F01; no new worker-command behavior. |
| F07 Tool conversation | — | No new model conversation. |
| F08 Authorization | — | No live judgment quality evaluation. |
| F09 Orchestration | — | No new PAT execution. |
| F10 Graph | — | No new live graph workflow. |
| F11 Supervision | — | No inference or supervision-quality claim. |
| F12 Context | — | Unchanged. |
| F13 Sessions | — | Historical confidence display preserved, no live resume. |
| F14 Verification | — | No model check/review/handoff run. |
| F15 User interaction | PASS, component | Settings/catalog regression controls reject ordinary chat judges and preserve workers. |
| F16 Evolution | — | Unchanged. |
| F17 Retrieval | — | Unchanged. |
| F18 Guidance | PASS, source | Current examples and guide describe native-only judges and configuration migration. |

Actual lifecycle regression fails on the old source for both Stop and launcher exit; corrected race tests pass. Unix explicitly detached sessions and abrupt harness SIGKILL remain outside the ownership guarantee. Native Windows Job behavior is not executed on this Mac.

## Validation result

Full Mac `go test -count=1 -timeout 600s ./...`: 31 tested packages passed, zero failures (runner 272.525s). A final reviewed sidecar-recipe allowlist adjustment was separately retested with the entire serve package (1.322s). Final build, vet, gofmt and diff checks passed. Race lifecycle controls passed; old-source rejection controls fail as expected for both cleanup and ordinary-chat judge configuration.

Windows amd64 and Linux amd64/arm64 pure-Go builds of serve/decision/config passed. The full application requires native Kuzu/CGO qualification; no native Windows/Linux application execution is claimed. Publication includes the shared code and this record on all three branches, preserving platform-only files.
