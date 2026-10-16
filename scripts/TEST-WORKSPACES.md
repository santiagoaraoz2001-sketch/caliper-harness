# Isolated live-test workspaces

Prepare each Snake or other coding-test run from its baseline before launching the
harness. A plain directory copy beneath another repository inherits that repository:
`git diff` can then inject unrelated project changes into the model's context.

```sh
python3 scripts/prepare-test-workspace.py /path/to/baseline /path/to/new-run/workspace
```

The destination must be new. The script copies the source without its Git metadata,
creates a fixture-local Git baseline, and verifies that the repository root is the
workspace and its status is clean. It leaves source fixtures and prior runs unchanged.
Inherited Git environment variables, commit hooks, and signing are not used to create
this disposable baseline. Only files eligible under the fixture's own ignore rules
are committed; generated dependencies remain ignored as usual.

Launch the harness from a shell without `GIT_DIR` or `GIT_WORK_TREE` overrides;
preparation sanitizes only its own Git subprocesses, not the launching shell.
Use the printed **Project root** as the harness working directory. Include that root
in the simulated user's run brief; use `git diff -- .` for project changes. This is
fixture preparation, not a product restriction on cross-project exploration.

For the existing Snake scenario, use the preserved baseline directory, not the
previous run's edited workspace:

```sh
python3 scripts/prepare-test-workspace.py \
  verification/2026-10-15-supervision-fixed-native/fixture/baseline \
  /tmp/caliper-snake-new-run/workspace
```

Regression test (no models or services):

```sh
python3 scripts/test-prepare-test-workspace.py
```

It reproduces the old dirty-parent diff, confirms the new workspace exposes only its
own edited file, checks inherited Git variables cannot redirect preparation, refuses
reuse of a prior run, and preserves the source Git metadata.
