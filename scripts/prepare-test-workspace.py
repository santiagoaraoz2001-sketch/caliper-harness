#!/usr/bin/env python3
"""Copy a test fixture into a new, independent Git workspace; never reuse a run."""

import argparse
import os
from pathlib import Path
import shutil
import subprocess


def git(workspace, *args):
    # An operator's enclosing Git worktree must not redirect fixture commands.
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    return subprocess.check_output(
        ["git", "-C", str(workspace), *args], env=env, text=True, stderr=subprocess.STDOUT
    ).strip()


def prepare(source, destination):
    source = Path(source).resolve(strict=True)
    destination = Path(destination).absolute()
    if not source.is_dir():
        raise ValueError("fixture source must be a directory")
    if destination.exists() or destination.is_symlink():
        raise ValueError("destination must be new; preserve existing runs")
    if destination.resolve().is_relative_to(source):
        raise ValueError("destination must be outside the source fixture")
    shutil.copytree(source, destination, symlinks=True, ignore=shutil.ignore_patterns(".git"))
    git(destination, "init", "--quiet", "--template=")
    # These settings belong only to this disposable test repository.
    git(destination, "config", "core.hooksPath", str(destination / ".git" / "disabled-hooks"))
    git(destination, "config", "commit.gpgSign", "false")
    git(destination, "config", "user.name", "Caliper fixture")
    git(destination, "config", "user.email", "fixture@localhost")
    git(destination, "add", "--all")
    git(destination, "commit", "--quiet", "--allow-empty", "-m", "Test fixture baseline")
    root = Path(git(destination, "rev-parse", "--show-toplevel")).resolve()
    if root != destination.resolve() or git(destination, "status", "--porcelain"):
        raise RuntimeError("fixture Git root or clean baseline verification failed")
    return root


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", help="existing baseline fixture (read only)")
    parser.add_argument("destination", help="new workspace for this run")
    args = parser.parse_args()
    root = prepare(args.source, args.destination)
    print(f"Project root: {root}")
    print(f"Repository root: {root}")
    print(f"Baseline: {git(root, 'rev-parse', 'HEAD')}")
    print("Run the harness from this directory. Inspect changes with git diff -- .")


if __name__ == "__main__":
    main()
