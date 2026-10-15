"""Component tests with explicit loader/model doubles; zero model inference."""
import sys
import tempfile
import unittest
from pathlib import Path
from functools import cached_property
from types import MethodType, SimpleNamespace
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import backends as b


def native(runtime="laya", label="yes"):
    answer = {"type": "choice", "choice": label, "probabilities": {"yes": .8, "no": .2},
              "confidence": .8, "answer_confidence": .63}
    return {"answers": {"pick": answer}, "usage": {"input_tokens": 10, "output_tokens": 0}}


class ParameterDouble:
    def __init__(self, device="cpu"):
        self.device = device
    def numel(self):
        return 123


class ModelDouble:
    """Models only canned native outputs; never loads weights or infers."""
    def __init__(self, raw=None):
        self.raw = native() if raw is None else raw
        self.device = "cpu"
        self.config = SimpleNamespace(max_position_embeddings=128)
        self.engine = SimpleNamespace(tokens=lambda *_: 10)
        self.calls = []
    def parameters(self):
        return iter([ParameterDouble(self.device)])
    def system_one(self, context, question):
        self.calls.append((context, question))
        return self.raw
    def to(self, device):
        self.device = device
        return self
    def eval(self):
        return self


class TokenizerDouble:
    """Character IDs, actual pinned Laya sequence builder; no model or tokenizer assets."""
    mask_token, mask_token_id, cls_token_id, sep_token_id = "[MASK]", 1, 2, 3
    def __call__(self, text, add_special_tokens=False, truncation=False, max_length=None):
        ids = [ord(c) + 10 for c in text]
        return {"input_ids": ids[:max_length] if truncation else ids}


class LayaDouble:
    """Uses native0.3.28 encoding methods; forward is explicitly canned."""
    def __init__(self, mutate=None):
        from laya.agent import Agent
        self.cfg = {"max_len": 128, "head_max_len": 96}
        self.tok, self.parallel_options, self.hooks = TokenizerDouble(), False, []
        self._to_internal = Agent._to_internal
        self._encode_state = MethodType(Agent._encode_state, self)
        self.model, self.calls, self.cpu_fallback_count = ModelDouble(), [], 0
        self.mutate = mutate
    def predict(self, context, questions):
        self.calls.append((context, questions))
        item = self._encode_state(context, ["pick"], {"pick": self._to_internal(questions["pick"])})[0]
        options = list(questions["pick"]["criteria"])
        raw = native(label=options[0])
        raw["answers"]["pick"]["probabilities"] = {o: .8 if i == 0 else .2 for i, o in enumerate(options)}
        raw["usage"].update(input_tokens=len(item["ids"]), **item["state_stats"])
        if self.mutate:
            self.mutate(raw)
        return raw


class NativeValidation(unittest.TestCase):
    def test_laya_native_confidence_is_not_d1_probability(self):
        self.assertEqual(b.typed_answer(native(), ["yes", "no"], "laya"), ("yes", .63))
        self.assertEqual(b.typed_answer(native(), ["yes", "no"], "system_one"), ("yes", .8))
    def test_bad_native_shapes_confidence_and_probabilities(self):
        cases = [None, {}, {"answers": []}]
        for field, value in [("type", "text"), ("choice", "unknown"), ("answer_confidence", float("nan")),
                             ("answer_confidence", True), ("answer_confidence", -1),
                             ("choice", "no"),
                             ("probabilities", {"yes": .8}), ("probabilities", {"yes": .9, "no": .9})]:
            raw = native(); raw["answers"]["pick"][field] = value; cases.append(raw)
        for raw in cases:
            with self.subTest(raw=raw), self.assertRaises(RuntimeError):
                b.typed_answer(raw, ["yes", "no"], "laya")
    def test_system_one_not_chat_and_consistent_probability(self):
        for field, value in [("confidence", .63), ("confidence", float("inf")), ("choice", "no")]:
            raw = native(); raw["answers"]["pick"][field] = value
            with self.subTest(field=field, value=value), self.assertRaises(RuntimeError):
                b.typed_answer(raw, ["yes", "no"], "system_one")
        for usage in ({}, {"input_tokens": True, "output_tokens": 0}, {"input_tokens": 1, "output_tokens": 1},
                      {"input_tokens": 1, "output_tokens": False}):
            raw = native(); raw["usage"] = usage
            with self.subTest(usage=usage), self.assertRaises(RuntimeError):
                b.typed_answer(raw, ["yes", "no"], "system_one")
    def test_laya_rounded_native_probabilities(self):
        raw = native(); raw["answers"]["pick"].update(choice="a", probabilities={"a": .3333, "b": .3333, "c": .3333})
        self.assertEqual(b.typed_answer(raw, ["a", "b", "c"], "laya"), ("a", .63))
    def test_laya_large_options_zero_or_gross_mass_refuses(self):
        options = [f"option{i}" for i in range(10001)]
        b.validate_judge({"model": "preferred", "context": "fixture", "options": options})
        for mass in (0., .9):
            raw = native()
            raw["answers"]["pick"].update(choice=options[0], probabilities={o: mass if i == 0 else 0. for i, o in enumerate(options)})
            with self.subTest(mass=mass), self.assertRaisesRegex(RuntimeError, "sum"):
                b.typed_answer(raw, options, "laya")
    def test_laya_mass_tolerance_cannot_allow_non_maximum_choice(self):
        raw = native(); raw["answers"]["pick"]["probabilities"] = {"yes": .4999, "no": .5001}
        with self.assertRaisesRegex(RuntimeError, "maximum"):
            b.typed_answer(raw, ["yes", "no"], "laya")
    def test_laya_large_options_exact_mass_has_no_count_allowlist(self):
        options = [f"option{i}" for i in range(10001)]
        raw = native()
        raw["answers"]["pick"].update(choice=options[0], probabilities={o: 1. if i == 0 else 0. for i, o in enumerate(options)})
        self.assertEqual(b.typed_answer(raw, options, "laya"), (options[0], .63))
    def test_judge_validation_before_any_model(self):
        base = {"model": "arbitrary/local", "context": "hi", "options": ["a", "b"]}
        for changes in ({"context": "  "}, {"options": ["a", "a"]}, {"options": ["a", False]},
                        {"instructions": 10}, {"device": "npu"}, {"model": None}):
            with self.subTest(changes=changes), self.assertRaises(b.BadRequest):
                b.validate_judge(dict(base, **changes))
    def test_open_selection_and_strict_controls(self):
        for name in ("some-org/third-compatible-checkpoint", "/other/assets", "local-copy"):
            self.assertEqual(b.Selection.from_body({"model": name}).model, name)
        for changes in ({"runtime": "chat"}, {"trust_remote_code": "false"}, {"path": 7}, {"unknown": True}):
            with self.subTest(changes=changes), self.assertRaises(b.BadRequest):
                b.Selection.from_body(changes)
    def test_actual_device_identity_mismatch_refuses(self):
        with self.assertRaisesRegex(RuntimeError, "device mismatch"):
            b.model_identity(ModelDouble(), "mps")


class LoaderControls(unittest.TestCase):
    def test_local_open_path_and_unknown_revision_are_truthful(self):
        with tempfile.TemporaryDirectory() as path:
            directory, source, revision = b.assets(b.Selection(model="any-preferred-id", path=path))
            self.assertEqual(directory, Path(path).resolve()); self.assertEqual(source, path); self.assertIsNone(revision)
            with self.assertRaisesRegex(b.BadRequest, "revision cannot be verified"):
                b.assets(b.Selection(path=path, revision="fiction"))
        with self.assertRaisesRegex(b.BadRequest, "missing local checkpoint"):
            b.assets(b.Selection(model=path))
    def test_cached_repo_and_library_alias_only_local_resolution(self):
        with tempfile.TemporaryDirectory() as root:
            directory = Path(root).resolve() / "snapshots" / "abc123"; directory.mkdir(parents=True)
            (directory / "typed").mkdir()
            hub = SimpleNamespace(snapshot_download=Mock(return_value=str(directory)))
            resolver = SimpleNamespace(resolve_model_spec=Mock(side_effect=lambda name: ("native/repo", "typed") if name == "alias" else None))
            with patch.dict(sys.modules, {"huggingface_hub": hub, "laya.router": resolver}):
                result = b.assets(b.Selection(model="unlisted/repo", revision="abc123"))
                hub.snapshot_download.assert_called_with("unlisted/repo", revision="abc123", local_files_only=True)
                self.assertEqual(result, (directory, "unlisted/repo", "abc123"))
                result = b.assets(b.Selection(model="alias")); self.assertEqual(result[0], directory / "typed")
    def test_laya_calls_public_load_local_path_not_router_allowlist(self):
        with tempfile.TemporaryDirectory() as path:
            directory = Path(path)
            for file in ("model.safetensors", "rl_agent_config.json"):
                (directory / file).touch()
            (directory / "tokenizer").mkdir()
            weights = ModelDouble()
            model = LayaDouble()
            model.cfg.update(max_len=512)
            model.model = weights
            model.predict = Mock(wraps=model.predict)
            load = Mock(return_value=model)
            with patch.dict(sys.modules, {"laya": SimpleNamespace(load=load)}):
                backend = b.load_backend(b.Selection(model="third-local-compatible", path=path, device="cpu"))
            load.assert_called_once_with(str(directory.resolve()), device="cpu")
            self.assertEqual(backend.identity["parameters"], 123)
            self.assertEqual(backend.judge("hi", ["yes", "no"], "choose")["confidence"], .63)
            self.assertEqual(model.predict.call_args.args[1]["pick"]["criteria"], {"yes": "yes", "no": "no"})
    def test_system_one_loader_uses_native_choice_and_explicit_trust(self):
        with tempfile.TemporaryDirectory() as path:
            model = ModelDouble()
            # Actual D1 config nests the language model's cap under text_config.
            model.config = SimpleNamespace(text_config=SimpleNamespace(max_position_embeddings=128))
            factory = Mock(return_value=(model, {}))
            choice = lambda instructions, options: (instructions, options)
            prompt_module = SimpleNamespace(Choice=choice)
            with patch.dict(sys.modules, {"transformers": SimpleNamespace(AutoModel=SimpleNamespace(from_pretrained=factory)),
                                         "torch": SimpleNamespace(float32="float32", bfloat16="bfloat16")}), patch.object(b.importlib, "import_module", return_value=prompt_module):
                backend = b.load_backend(b.Selection(model="unlisted/native", path=path, runtime="system_one", device="cpu", trust_remote_code=True))
            factory.assert_called_once_with(str(Path(path).resolve()), trust_remote_code=True, local_files_only=True, dtype="float32", output_loading_info=True)
            self.assertEqual(backend.judge("hi", ["yes", "no"], "choose")["confidence"], .8)
            model.engine.tokens = lambda *_: 129
            with self.assertRaisesRegex(b.BadRequest, "context limit"):
                backend.judge("hi", ["yes", "no"], "choose")
            self.assertEqual(len(model.calls), 1)
    def test_system_one_lazy_engine_initializes_after_device_placement(self):
        class LazyDeviceModelDouble(ModelDouble):
            def __init__(self):
                super().__init__()
                del self.engine
                self.order = []
            @cached_property
            def engine(self):
                self.order.append(("engine", self.device))
                return SimpleNamespace(device=self.device, tokens=lambda *_: 10)
            def to(self, device):
                self.order.append(("to", device))
                return super().to(device)
        with tempfile.TemporaryDirectory() as path:
            model = LazyDeviceModelDouble()
            factory = Mock(return_value=(model, {}))
            with patch.dict(sys.modules, {"transformers": SimpleNamespace(AutoModel=SimpleNamespace(from_pretrained=factory)),
                                         "torch": SimpleNamespace(float32="float32", bfloat16="bfloat16")}), patch.object(b, "resolve", return_value=("torch", "mps")), patch.object(b.importlib, "import_module", return_value=SimpleNamespace(Choice=lambda *a: a)):
                backend = b.load_backend(b.Selection(path=path, runtime="system_one", device="gpu", trust_remote_code=True))
            self.assertEqual(model.order, [("to", "mps"), ("engine", "mps")])
            self.assertEqual(model.engine.device, "mps")
            self.assertEqual(backend.identity["native_device"], "mps")
    def test_system_one_generic_or_weight_mismatch_refuses(self):
        with tempfile.TemporaryDirectory() as path:
            for model, info, error in [(ModelDouble(), {"missing_keys": ["weight"]}, RuntimeError), (SimpleNamespace(), {}, b.BadRequest)]:
                with self.subTest(info=info), patch.dict(sys.modules, {
                    "torch": SimpleNamespace(float32="float32", bfloat16="bfloat16"),
                    "transformers": SimpleNamespace(AutoModel=SimpleNamespace(from_pretrained=lambda *a, **k: (model, info)))}), self.assertRaises(error):
                    b.load_backend(b.Selection(path=path, runtime="system_one", device="cpu"))
    def test_laya_fallback_refuses_wrong_device_claim(self):
        weights = ModelDouble()
        model = LayaDouble()
        model.model = weights
        def predict(*_):
            model.cpu_fallback_count += 1
            return native()
        model.predict = predict
        backend = b.Backend(b.Selection(), model, {"native_device": "cpu", "device": "cpu", "context_limit":128})
        with self.assertRaisesRegex(RuntimeError, "CPU fallback"):
            backend.judge("hi", ["yes", "no"], "choose")


class FullInputControls(unittest.TestCase):
    def laya_backend(self, model):
        return b.Backend(b.Selection(), model, {"native_device": "cpu", "device": "cpu", "context_limit": model.cfg["max_len"]})
    def test_laya_exact_fit_and_overflow_before_forward(self):
        model = LayaDouble()
        backend = self.laya_backend(model)
        question = {"type": "choice", "instructions": "choose", "criteria": {"yes": "yes", "no": "no"}}
        base = model._encode_state("", ["pick"], {"pick": model._to_internal(question)})[0]
        room = model.cfg["max_len"] - len(base["ids"])
        self.assertEqual(backend.judge("x" * room, ["yes", "no"], "choose")["option"], "yes")
        self.assertEqual(len(model.calls), 1)
        model.calls.clear()
        with self.assertRaisesRegex(b.BadRequest, "fit|dropped|context"):
            backend.judge("x" * (room+1), ["yes", "no"], "choose")
        self.assertEqual(model.calls, [])
    def test_laya_head_fields_cannot_be_silently_clipped(self):
        for instructions, options in [("x"*100, ["yes", "no"]), ("choose", ["x"*60, "no"])]:
            model = LayaDouble()
            with self.subTest(instructions=instructions, options=options):
                with self.assertRaises(b.BadRequest):
                    self.laya_backend(model).judge("hi", options, instructions)
                self.assertEqual(model.calls, [])
    def test_laya_installed_hooks_refuse_before_forward(self):
        model = LayaDouble()
        model.hooks = [lambda ctx: None]
        with self.assertRaisesRegex(RuntimeError, "hooks"):
            self.laya_backend(model).judge("hi", ["yes", "no"], "choose")
        self.assertEqual(model.calls, [])
    def test_laya_actual_usage_must_prove_full_input(self):
        cases = [{"state_tokens_dropped": 1}, {"truncated": True}, {}, {"input_tokens": 0},
                 {"input_tokens": True}, {"state_tokens": 0}, {"truncated": 0},
                 {"output_tokens": 1}, {"state_tokens": 99}]
        for changes in cases:
            def mutate(raw):
                if not changes: raw.pop("usage")
                else: raw["usage"].update(changes)
            model = LayaDouble(mutate)
            with self.subTest(changes=changes), self.assertRaises(RuntimeError):
                self.laya_backend(model).judge("hi", ["yes", "no"], "choose")
            self.assertEqual(len(model.calls), 1)
    def test_system_one_postusage_cap_and_explicit_truncation(self):
        for usage in [{"input_tokens": 129, "output_tokens": 0},
                      {"input_tokens": 10, "output_tokens": 0, "truncated": True},
                      {"input_tokens": 10, "output_tokens": 0, "state_tokens_dropped": 1}]:
            raw = native(); raw["usage"] = usage
            model = ModelDouble(raw)
            backend = b.Backend(b.Selection(runtime="system_one"), model, {"native_device":"cpu", "device":"cpu", "context_limit":128}, lambda *a:a)
            with self.subTest(usage=usage), self.assertRaises(RuntimeError):
                backend.judge("hi", ["yes", "no"], "choose")
            self.assertEqual(len(model.calls), 1)


if __name__ == "__main__":
    unittest.main()
