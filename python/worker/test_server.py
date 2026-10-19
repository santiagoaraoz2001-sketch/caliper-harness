"""Neutral-thread components and native class admission; no model construction/loading."""
import io
import json
import os
import types
import tempfile
import threading
import importlib
from unittest.mock import patch
import sys
import unittest
import server


class NativeHooks(unittest.TestCase):
    def native(self):
        class Provider:
            def __init__(self):
                self.cli_args = types.SimpleNamespace(adapter_path=None, draft_model=None)
                self.model_key = None
                self.model = self.tokenizer = None
                self.calls = []
            def load(self, model, adapter=None, draft=None):
                self.calls.append((model, adapter, draft))
                self.model_key = ("/canonical/local", None, None)
                self.model, self.tokenizer = object(), object()
                return self.model, self.tokenizer
        class Generator:
            def _tokenize(self, *args):
                raise AssertionError("no tokenization in bootstrap control")
            def __init__(self):
                self._generation_thread = threading.Thread(target=lambda: None)
                self._stop = False
            def generate(self):
                raise AssertionError("no generation in component control")
            def _generate(self):
                raise AssertionError("no generation in component control")
            def join(self):
                self._generation_thread.join()
            def stop_and_join(self):
                self._stop = True
                self.join()
        class Handler:
            def handle_chat_completions(self):
                raise AssertionError("no chat in bootstrap control")
            def handle_completion(self, request, stop_words):
                raise AssertionError("no completion in bootstrap control")
            def handle_health_check(self):
                raise AssertionError("original native health must be replaced")
            def _set_completion_headers(self, status):
                self.status = status
            def end_headers(self):
                pass
        return types.SimpleNamespace(ModelProvider=Provider, ResponseGenerator=Generator,
                                     APIHandler=Handler, _make_logits_processors=lambda args: [], mx=types.SimpleNamespace(default_device=lambda: "component-device"))

    def test_catalog_is_selected_checkpoint_without_cache_or_loading(self):
        native = self.native()
        server.install_native_hooks(native, "/canonical/local")
        with tempfile.TemporaryDirectory() as home, patch.dict(os.environ, {"HOME": home, "HF_HUB_CACHE": home + "/does-not-exist"}):
            h = native.APIHandler()
            h.wfile = io.BytesIO()
            h.handle_models_request()
            self.assertEqual(h.status, 200)
            self.assertEqual(json.loads(h.wfile.getvalue()), {"object": "list", "data": [{"id": "/canonical/local", "object": "model", "owned_by": "mlx"}]})
            self.assertFalse(os.path.exists(home + "/does-not-exist"))

    def test_native_call_identity_and_no_unknown_resolution(self):
        native = self.native()
        server.install_native_hooks(native, "/canonical/local")
        provider = native.ModelProvider()
        provider.load("default_model", None, "default_model")
        self.assertEqual(provider.calls, [("default_model", None, "default_model")])
        for args in [("network/repo",), ("/other/local",), ("default_model", "adapter"), ("default_model", None, "draft")]:
            with self.assertRaises(ValueError):
                provider.load(*args)
        self.assertEqual(len(provider.calls), 1)

    def test_actual_fact_combinations_not_inventory_or_pid(self):
        native = self.native()
        server.install_native_hooks(native, "/canonical/local")
        p = native.ModelProvider()
        g = native.ResponseGenerator()
        g.model_provider = p
        release = threading.Event()
        live = threading.Thread(target=release.wait)
        live.start()
        self.addCleanup(live.join)
        self.addCleanup(release.set)
        dead = threading.Thread(target=lambda: None)
        dead.start()
        dead.join()
        h = native.APIHandler()
        h.response_generator = g
        for loaded, tokenizer, alive, expected in [(False, False, True, 503), (True, False, True, 503), (True, True, False, 503), (True, True, True, 200)]:
            p.model_key = ("/canonical/local", None, None) if loaded else None
            p.model = object() if loaded else None
            p.tokenizer = object() if tokenizer else None
            g._generation_thread = live if alive else dead
            h.wfile = io.BytesIO()
            h.handle_health_check()
            self.assertEqual(h.status, expected)
            self.assertEqual(json.loads(h.wfile.getvalue())["pid"], os.getpid())
        p.model_key = ("wrong", None, None)
        h.wfile = io.BytesIO()
        h.handle_health_check()
        self.assertEqual(h.status, 503)
        p.model_key = ("/canonical/local", None, None)
        g._stop = True
        h.wfile = io.BytesIO()
        h.handle_health_check()
        self.assertEqual(h.status, 503)
        self.assertFalse(json.loads(h.wfile.getvalue())["generation"])

    def test_unknown_lifecycle_shape_refuses(self):
        native = self.native()
        server.install_native_hooks(native, "/canonical/local")
        g = native.ResponseGenerator()
        g.model_provider = native.ModelProvider()
        h = native.APIHandler()
        h.response_generator, h.wfile = g, io.BytesIO()
        for attr, value in [("_generation_thread", object()), ("_stop", None), ("_stop", 0)]:
            with patch.object(g, attr, value), self.assertRaisesRegex(RuntimeError, "unsupported.*lifecycle"):
                h.handle_health_check()
        self.assertEqual(h.wfile.getvalue(), b"")
        native.ResponseGenerator.generate = None
        with self.assertRaisesRegex(RuntimeError, "unsupported"):
            server.install_native_hooks(native, "/canonical/local")

    def test_installed_native_class_api_admission_without_instances(self):
        native = importlib.import_module("mlx_lm.server")
        original_load = native.ModelProvider.load
        original_health = native.APIHandler.handle_health_check
        original_models = native.APIHandler.handle_models_request
        with patch.object(native.ResponseGenerator, "__init__", side_effect=AssertionError("must not construct native generator")), patch.object(native.ModelProvider, "__init__", side_effect=AssertionError("must not construct native provider")):
            try:
                server.install_native_hooks(native, "/canonical/local")
                self.assertIsNot(native.ModelProvider.load, original_load)
                self.assertIsNot(native.APIHandler.handle_health_check, original_health)
            finally:
                native.ModelProvider.load = original_load
                native.APIHandler.handle_health_check = original_health
                native.APIHandler.handle_models_request = original_models

    def test_entry_preserves_native_arguments_and_offline_before_import(self):
        native = self.native()
        calls = []
        native.main = lambda: calls.append((list(sys.argv), os.environ.get("HF_HUB_OFFLINE"), os.environ.get("TRANSFORMERS_OFFLINE")))
        package = types.ModuleType("mlx_lm")
        package.server = native
        with tempfile.TemporaryDirectory() as folder, patch.dict(sys.modules, {"mlx_lm": package, "mlx_lm.server": native}), patch.dict(os.environ, {}, clear=False), patch.object(sys, "argv", ["bootstrap", "--model", folder, "--port", "18440", "--temp", "0.3"]):
            server.main()
            self.assertEqual(calls, [(["bootstrap", "--model", str(server.Path(folder).resolve()), "--port", "18440", "--temp", "0.3"], "1", "1")])
        with patch.object(sys, "argv", ["bootstrap", "--model", "missing-unresolved-network-id"]):
            with self.assertRaises(FileNotFoundError):
                server.main()

    def test_incompatible_native_api_is_loud(self):
        with self.assertRaisesRegex(RuntimeError, "unsupported"):
            server.install_native_hooks(types.SimpleNamespace(), "/canonical/local")


if __name__ == "__main__":
    unittest.main()
