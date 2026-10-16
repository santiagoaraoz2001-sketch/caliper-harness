#!/usr/bin/env python3
"""Regression checks for parent-repository contamination in live test fixtures."""

import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "prepare_workspace", Path(__file__).with_name("prepare-test-workspace.py")
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class WorkspaceTest(unittest.TestCase):
    def test_dirty_parent_is_not_part_of_fixture_diff(self):
        with tempfile.TemporaryDirectory() as tmp:
            parent = Path(tmp).resolve()
            module.git(parent, "init", "--quiet", "--template=")
            unrelated = parent / "unrelated.txt"
            unrelated.write_text("original\n")
            module.git(parent, "add", "--all")
            unrelated.write_text("unrelated change\n")
            source = parent / "baseline"
            source.mkdir()
            (source / "game.js").write_text("original game\n")
            destination = parent / "fresh-run"
            destination.mkdir()
            # Historical copy-only setup inherits the enclosing repository.
            self.assertEqual(Path(module.git(destination, "rev-parse", "--show-toplevel")), parent)
            self.assertIn("unrelated change", module.git(destination, "diff"))
            destination.rmdir()
            with patch.dict(os.environ, {"GIT_DIR": str(parent / ".git"), "GIT_WORK_TREE": str(parent)}):
                root = module.prepare(source, destination)
            self.assertEqual(root, destination)
            self.assertEqual(module.git(root, "diff", "HEAD"), "")
            (root / "game.js").write_text("fixed game\n")
            diff = module.git(root, "diff")
            self.assertIn("fixed game", diff)
            self.assertNotIn("unrelated", diff)
            self.assertEqual(module.git(root, "diff", "--name-only"), "game.js")
            self.assertEqual((source / "game.js").read_text(), "original game\n")
            with self.assertRaisesRegex(ValueError, "destination must be new"):
                module.prepare(source, destination)
            self.assertEqual((root / "game.js").read_text(), "fixed game\n")

    def test_source_git_history_is_not_copied(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            source = base / "source"
            source.mkdir()
            module.git(source, "init", "--quiet", "--template=")
            (source / ".git" / "source-marker").write_text("preserve me")
            root = module.prepare(source, base / "destination")
            self.assertFalse((root / ".git" / "source-marker").exists())
            self.assertTrue((source / ".git" / "source-marker").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
