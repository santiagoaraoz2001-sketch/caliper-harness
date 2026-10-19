"""Prompt sharing and startup tests. Backend calls are explicit component doubles."""
import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import prompts
import server
import yaml
from backends import Selection, validate_judge
from test_server import BackendDouble, body


class PromptControls(unittest.TestCase):
    def selected(self, directory):
        data = yaml.safe_load(Path(prompts.load().source).read_text())
        data["texts"].update({"decision.default.instructions": "Selected catalog default.",
                              "decision.readiness_probe.context": "Selected readiness context.",
                              "decision.readiness_probe.instructions": "Selected readiness instructions."})
        path = Path(directory) / "selected.yaml"
        path.write_text(yaml.safe_dump(data), encoding="utf-8")
        return path

    def test_default_is_same_shipped_go_asset(self):
        catalog = prompts.load()
        canonical = Path(__file__).resolve().parents[2] / "internal/prompts/prompts.yaml"
        self.assertEqual(Path(catalog.source), canonical.resolve())
        self.assertEqual(dict(catalog.texts), yaml.safe_load(canonical.read_text())["texts"])

    def test_legacy_catalog_without_optional_setup_guide_remains_valid(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.selected(directory)
            data = yaml.safe_load(path.read_text())
            del data['texts']['setup.assisted']
            path.write_text(yaml.safe_dump(data), encoding='utf-8')
            catalog = prompts.load(path)
            self.assertEqual(catalog.text('decision.default.instructions'), 'Selected catalog default.')
            self.assertNotIn('setup.assisted', catalog.texts)

    def test_manager_uses_one_selected_snapshot_for_probe_and_fallback(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.selected(directory)
            catalog = prompts.load(path)
            manager = server.Manager(BackendDouble, catalog)
            manager.activate(Selection())
            self.assertEqual(manager.active.calls[0], ("Selected readiness context.", ["four", "five"], "Selected readiness instructions."))
            path.write_text("invalid after snapshot", encoding="utf-8")
            for instructions in (None, ""):
                manager.judge(body(instructions=instructions))
                self.assertEqual(manager.active.calls[-1][2], "Selected catalog default.")
            manager.judge(body(instructions="Actual request instruction."))
            self.assertEqual(manager.active.calls[-1][2], "Actual request instruction.")
            self.assertEqual(validate_judge(body(), catalog)[2], "Selected catalog default.")
            with self.assertRaises(TypeError):
                catalog.texts["decision.default.instructions"] = "mutated"

    def test_missing_invalid_and_static_templates_refuse(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "catalog.yaml"
            with self.assertRaisesRegex(ValueError, "prompt catalog"):
                prompts.load(path)
            invalid = ("texts: [not, a, mapping]", "texts: {}", "texts: {a: '', a: duplicate}",
                       "texts: {a: yes}", "texts: {}\nother: forbidden", "texts: {}\n---\ntexts: {}")
            for content in invalid:
                path.write_text(content)
                with self.subTest(content=content), self.assertRaises(ValueError):
                    prompts.load(path)
            path = self.selected(directory)
            data = yaml.safe_load(path.read_text())
            data["texts"]["decision.default.instructions"] = "{{.Undeclared}}"
            path.write_text(yaml.safe_dump(data))
            with self.assertRaisesRegex(ValueError, "static Python prompt"):
                prompts.load(path)

    def test_explicit_cli_failure_precedes_backend_load(self):
        with tempfile.TemporaryDirectory() as directory, patch.object(server, "load_backend") as loader:
            bad = Path(directory) / "missing.yaml"
            with patch.object(sys, "argv", [server.__file__, "--prompts-file", str(bad)]), contextlib.redirect_stderr(io.StringIO()) as stderr:
                self.assertEqual(server.main(), 1)
            self.assertIn("prompt catalog", stderr.getvalue())
            loader.assert_not_called()

    def test_cli_selected_file_reaches_fake_startup(self):
        with tempfile.TemporaryDirectory() as directory:
            path = self.selected(directory)
            # main owns startup catalog loading. Manager's default native loader
            # is replaced only for this component test; no checkpoint is loaded.
            class FakeManager(server.Manager):
                def __init__(self, catalog=None):
                    super().__init__(BackendDouble, catalog)
            real_load = server.load_prompts
            def selected_only(selected):
                self.assertEqual(selected, str(path))  # never load an absent unrelated default first
                return real_load(selected)
            with patch.object(server, "MANAGER", None), patch.object(server, "Manager", FakeManager), patch.object(server, "load_prompts", side_effect=selected_only) as loaded, patch.object(server, "ThreadingHTTPServer") as http, patch.object(sys, "argv", [server.__file__, "--prompts-file", str(path)]), contextlib.redirect_stdout(io.StringIO()) as stdout:
                self.assertEqual(server.main(), 0)
                loaded.assert_called_once_with(str(path))
                self.assertEqual(server.MANAGER.active.calls[0][0], "Selected readiness context.")
                self.assertEqual(json.loads(stdout.getvalue())["health"]["status"], "ok")
                http.return_value.serve_forever.assert_called_once()


if __name__ == "__main__":
    unittest.main()
