"""Sidecar routing tests (stdlib only). No judgment runs here — resolve()
and request validation only, so no checkpoint loads."""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import server

CTX = "The user was billed twice and asks for a refund."
OPTS = ["approve refund", "deny refund"]


def body(**kw):
    b = {"model": "typed-decisions", "context": CTX, "options": OPTS}
    b.update(kw)
    return b


class RoutingTest(unittest.TestCase):
    def test_auto_resolves_torch(self):
        engine, target = server.resolve("auto")
        self.assertEqual(engine, "torch")
        self.assertIn(target, ("cuda", "mps", "cpu"))

    def test_cpu_resolves(self):
        self.assertEqual(server.resolve("cpu"), ("torch", "cpu"))

    def test_unknown_device_is_bad_request(self):
        with self.assertRaises(server.BadRequest):
            server.resolve("tpu")

    def test_npu_retired_is_bad_request(self):
        with self.assertRaises(server.BadRequest) as cm:
            server.resolve("npu")
        self.assertIn("future/npu", str(cm.exception))

    def test_model_skew_is_bad_request(self):
        with self.assertRaises(server.BadRequest):
            server.judge(body(model="wrong"))

    def test_empty_context_is_bad_request(self):
        with self.assertRaises(server.BadRequest):
            server.judge(body(context=""))

    def test_single_option_is_bad_request(self):
        with self.assertRaises(server.BadRequest):
            server.judge(body(options=["only"]))

    def test_blank_option_is_bad_request(self):
        with self.assertRaises(server.BadRequest):
            server.judge(body(options=["ok", ""]))


# Explicit component doubles below: no checkpoint loading or model inference.
import io
import json
import subprocess
import tempfile
import threading
from pathlib import Path
from unittest.mock import patch
from backends import Selection
from backends import Backend


class BackendDouble:
    def __init__(self, selection, fail=False):
        self.selection = selection
        self.identity = {"model": selection.model, "path": "/owned/double", "revision": None,
                         "runtime": selection.runtime, "device": "cpu", "native_device": "cpu", "parameters": 123}
        self.fail, self.calls = fail, []
        self.entered, self.release = threading.Event(), threading.Event()
        self.block = False
    def judge(self, context, options, instructions):
        self.calls.append((context, options, instructions))
        if self.fail:
            raise RuntimeError("double native forward refused")
        if self.block:
            self.entered.set()
            if not self.release.wait(3):
                raise RuntimeError("test drain timed out")
        return {"pick": 0, "option": options[0], "confidence": .4,
                "model": self.selection.model, "device": "cpu"}


class ActivationControls(unittest.TestCase):
    def test_health_unloaded_and_open_selection_admission(self):
        manager = server.Manager(BackendDouble)
        self.assertEqual(manager.health()["model"], None)
        self.assertFalse(manager.health()["ready"])
        selection = Selection(model="unlisted/third-native", runtime="system_one")
        health = manager.activate(selection)
        self.assertTrue(health["loaded"])
        self.assertEqual(health["requested"], selection.describe())
        self.assertEqual(health["active"]["model"], selection.model)
        self.assertEqual(len(manager.active.calls), 1)  # readiness forward required
        result = manager.judge(body(model=selection.model))
        self.assertEqual(set(result), {"pick", "option", "confidence", "model", "device"})
        self.assertEqual(result["confidence"], .4)  # low confidence still native readiness
    def test_failed_load_or_forward_keeps_known_good_active(self):
        manager = server.Manager(BackendDouble)
        manager.activate(Selection(model="good"))
        active = manager.active
        for load in (lambda _: (_ for _ in ()).throw(RuntimeError("missing weight")), lambda s: BackendDouble(s, fail=True)):
            manager.loader = load
            with self.assertRaises(RuntimeError):
                manager.activate(Selection(model="candidate"))
            health = manager.health()
            self.assertIs(manager.active, active)
            self.assertTrue(health["ready"])
            self.assertFalse(health["loading"])
            self.assertEqual(health["model"], "good")
            self.assertEqual(health["requested"]["model"], "candidate")
            self.assertIsNotNone(health["error"])
            self.assertEqual(manager.judge(body(model="good"))["model"], "good")
    def test_real_normalizer_refuses_nan_candidate_before_swap(self):
        # Actual production Backend + Manager, with only weights/forward doubled.
        from test_backends import LayaDouble
        manager = server.Manager(BackendDouble); manager.activate(Selection(model="good"))
        def loader(selection):
            model = LayaDouble(mutate=lambda raw: raw["answers"]["pick"].update(answer_confidence=float("nan")))
            candidate_models.append(model)
            return Backend(selection, model, {"native_device": "cpu", "device": "cpu", "context_limit": model.cfg["max_len"]})
        candidate_models = []
        manager.loader = loader
        with self.assertRaisesRegex(RuntimeError, "confidence"):
            manager.activate(Selection(model="nan-candidate"))
        self.assertEqual(len(candidate_models[0].calls), 1)  # Actual forward double reached before NaN refusal.
        self.assertEqual(manager.health()["model"], "good")
        self.assertEqual(manager.judge(body(model="good"))["model"], "good")
    def test_loading_leaves_active_usable_and_rejects_second_activation(self):
        manager = server.Manager(BackendDouble); manager.activate(Selection(model="old"))
        entered, release, errors = threading.Event(), threading.Event(), []
        def loading(selection):
            entered.set(); release.wait(3); return BackendDouble(selection)
        manager.loader = loading
        def activate():
            try: manager.activate(Selection(model="next"))
            except Exception as error: errors.append(error)
        thread = threading.Thread(target=activate); thread.start()
        try:
            self.assertTrue(entered.wait(1)); self.assertTrue(manager.health()["loading"])
            self.assertEqual(manager.judge(body(model="old"))["model"], "old")
            with self.assertRaisesRegex(server.BadRequest, "in progress"):
                manager.activate(Selection(model="other"))
            self.assertEqual(manager.health()["requested"]["model"], "next")
        finally:
            release.set(); thread.join(3)
        self.assertFalse(thread.is_alive()); self.assertEqual(errors, [])
        self.assertEqual(manager.health()["model"], "next")
    def test_swap_drains_old_forward_and_health_does_not_wait(self):
        manager = server.Manager(BackendDouble); manager.activate(Selection(model="old"))
        old = manager.active; old.block = True; errors = []
        def call():
            try: manager.judge(body(model="old"))
            except Exception as error: errors.append(error)
        judgment = threading.Thread(target=call); judgment.start()
        self.assertTrue(old.entered.wait(1))
        warmed = threading.Event()
        class CandidateDouble(BackendDouble):
            def judge(self, *args):
                result = super().judge(*args); warmed.set(); return result
        manager.loader = CandidateDouble
        def activate():
            try: manager.activate(Selection(model="new"))
            except Exception as error: errors.append(error)
        swap = threading.Thread(target=activate); swap.start()
        try:
            self.assertTrue(warmed.wait(1))
            self.assertEqual(manager.health()["model"], "old")
            self.assertTrue(swap.is_alive())
        finally:
            old.release.set(); judgment.join(3); swap.join(3)
        self.assertEqual(errors, []); self.assertFalse(swap.is_alive())
        self.assertEqual(manager.health()["model"], "new")
        with self.assertRaisesRegex(server.BadRequest, "serves"):
            manager.judge(body(model="old"))
    def test_active_device_skew_refuses_without_forward(self):
        manager = server.Manager(BackendDouble); manager.activate(Selection())
        with self.assertRaisesRegex(server.BadRequest, "device"):
            manager.judge(body(device="gpu"))
        self.assertEqual(len(manager.active.calls), 1)
    def test_public_cli_missing_local_path_useful_exit(self):
        path = str(Path(tempfile.gettempdir()) / "specific-harness-r31-no-checkpoint-619ef5")
        result = subprocess.run([sys.executable, server.__file__, "--model", path], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 1)
        self.assertIn("missing local checkpoint", result.stderr)
        self.assertEqual(result.stdout, "")


class HTTPControls(unittest.TestCase):
    def handler(self, path, payload, length=None):
        handler = object.__new__(server.Handler)
        handler.path = path
        data = json.dumps(payload).encode()
        handler.rfile = io.BytesIO(data)
        handler.headers = {"Content-Length": str(len(data) if length is None else length)}
        handler.sent = []
        handler._send = lambda status, obj: handler.sent.append((status, obj))
        return handler
    def test_health_and_activation_and_judge_use_actual_handlers(self):
        manager = server.Manager(BackendDouble)
        with patch.object(server, "MANAGER", manager):
            h = self.handler("/health", None); h.do_GET()
            self.assertFalse(h.sent[0][1]["loaded"])
            h = self.handler("/activate", {"model": "arbitrary/native"}); h.do_POST()
            self.assertEqual(h.sent[0][0], 200)
            h = self.handler("/judge", body(model="arbitrary/native")); h.do_POST()
            self.assertEqual(h.sent[0][1]["model"], "arbitrary/native")
            h = self.handler("/judge", body(model="wrong")); h.do_POST()
            self.assertEqual(h.sent[0][0], 400)
    def test_malformed_activation_does_not_mutate_known_good(self):
        manager = server.Manager(BackendDouble); manager.activate(Selection(model="good"))
        for payload in ([], {"runtime": "chat"}, {"trust_remote_code": 1}, {"unexpected": True}):
            with self.subTest(payload=payload), patch.object(server, "MANAGER", manager):
                h = self.handler("/activate", payload); h.do_POST()
                self.assertEqual(h.sent[0][0], 400)
                self.assertEqual(manager.health()["model"], "good")
    def test_body_length_and_json_errors(self):
        for length in (0, 1048577, 1000):
            h = self.handler("/judge", None, length); h.do_POST()
            self.assertEqual(h.sent[0][0], 400)


class BindingControls(unittest.TestCase):
    def test_health_reports_pid_and_admitted_not_failed_selection(self):
        manager = server.Manager(BackendDouble)
        admitted = Selection(model='stable', path='/old', revision='old')
        manager.activate(admitted)
        manager.loader = lambda s: BackendDouble(s, fail=True)
        with self.assertRaises(RuntimeError): manager.activate(Selection(model='stable', path='/new'))
        health = manager.health()
        self.assertEqual(health.get('pid'), os.getpid())
        self.assertEqual(health['active'].get('selection'), admitted.describe())
        self.assertEqual(health['requested']['path'], '/new')
    def test_optional_expected_binding_is_checked_before_forward(self):
        manager = server.Manager(BackendDouble)
        admitted = Selection(model='stable', path='/old')
        manager.activate(admitted)
        self.assertEqual(manager.judge(body(model='stable'))['model'], 'stable')
        self.assertEqual(manager.judge(body(model='stable', expected=admitted.describe()))['model'], 'stable')
        before = len(manager.active.calls)
        for change in ({'path':'/new'}, {'runtime':'system_one'}, {'revision':'other'}, {'trust_remote_code':True}, {'device':'gpu'}):
            expected = dict(admitted.describe(), **change)
            with self.subTest(change=change), self.assertRaises(server.BadRequest):
                manager.judge(body(model='stable', expected=expected))
        self.assertEqual(len(manager.active.calls), before)

if __name__ == "__main__":
    unittest.main()
