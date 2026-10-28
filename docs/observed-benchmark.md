# Observed benchmark controller

The controller starts one new Caliper TUI, enables `/tools full`, submits the supplied prompt, and leaves the TUI open after settlement for further user feedback. It does not start an inference server itself, alter the game's files, decide user answers, or infer success from model prose. Use a fresh game directory and a config selecting the intended existing inference connections and 262,144-token context.

```powershell
python scripts/run-observed-benchmark.py --exe C:/path/caliper.exe --workspace C:/path/new-game --config C:/path/benchmark.json --prompt-file C:/path/prompt.txt --output-dir "$env:TEMP/caliper-benchmark/run-001"
```

The output directory must be under system TEMP and must not contain a previous controller run. The supplied config is copied to `effective-config.yaml`, with `sessions.path` set to `<output-dir>/sessions`. JSON configs can already contain a sessions mapping; YAML configs must omit it to avoid duplicate-key ambiguity without another parser dependency. Use absolute paths for other explicit config paths. Other settings and saved personal connections are preserved.

The controlling user agent reads `status.json` and writes ordered UTF-8 files to `inbox`, for example `0001.json`:

```json
{"id":"0001","text":"/btw Please make docking feedback clear before adding more destinations."}
```

Write each file to a temporary name and rename it to `.json` once complete. IDs must be unique. Input is exact text plus a real Enter; multiline text uses bracketed paste before Enter. Ordinary prompts belong at idle, `/btw` or `/comment <task-id> ...` interjects during a run. An ask dialog with free text accepts ordinary text. For option dialogs, use `\u001b[B`/`\u001b[A` to change selection, `\t` to enter a comment, and text; the controller supplies Enter. Empty text submits the selected option. Read the current screen/question instead of guessing the selection.

`receipts.jsonl` distinguishes `sent` from `trace_confirmed`. A moved inbox file proves only that it was sent. Confirmation requires the main session's prompt, user note, task-comment delivery, or actual ask result. Native asks are identified from `native_reply` and the actual `call_resolution`; submitted answers retain that call ID. Only the matching `native_result` with an actual formatted answer confirms them. Keyboard-only selections are confirmed by that matching result, with its result excerpt recorded; typed answers/comments must also appear in it. A different tool, worker result or later user note cannot confirm an ask answer. UI-only commands may never produce a trace confirmation. The controller never resends an uncertain input automatically.

`status.json` contains the current main session, tool counts, latest trace event, last ask/result, latest reply/final text, host outcome, pending deliveries and a bounded terminal tail. `done`, `cancelled`, `error`, `failed` and `stopped` all settle a main run; child-session outcomes do not. Each later ordinary prompt is associated with its new session through its recorded prompt. `last_final` includes native assistant content without tool calls and may contain model prose; `host_outcome` is the separate host settlement. A `done` event is not a substitute for independently assessing game quality.

To close the owned TUI after settlement, create `quit.json` in the output directory. It waits while a main run or ordinary follow-up submission is active. There is no time-based failure cutoff. If startup never reaches the composer or a tool-view confirmation, status stays in the corresponding phase so the operator can inspect the actual fault.

Raw PTY bytes append to `terminal.raw`; full session JSONL remains in `sessions`. In-memory screen text is bounded; session files are tailed from offsets. Events larger than 1 MiB get a marked summary rather than loading huge context snapshots into controller memory; their original bytes remain in the trace file. A controller crash is not a harness success and its status records whether its TUI was still alive before owned-process cleanup.

Controller-only tests require no model or TUI:

```powershell
python scripts/test-observed-benchmark.py
```
