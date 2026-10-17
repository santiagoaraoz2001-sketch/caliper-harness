"""Native typed-choice adapters; no generated answers or invented confidence.

Selection is open within each runtime's compatible architectures. Assets must
already be downloaded; remote checkpoint Python requires explicit opt-in.
"""
import importlib
import math
from dataclasses import asdict, dataclass
from pathlib import Path
from prompts import load as load_prompts


class BadRequest(Exception):
    pass


@dataclass(frozen=True)
class Selection:
    model: str = "typed-decisions"
    runtime: str = "laya"
    device: str = "auto"
    path: str | None = None
    revision: str | None = None
    trust_remote_code: bool = False

    @classmethod
    def from_body(cls, body):
        if not isinstance(body, dict):
            raise BadRequest("activation body must be a JSON object")
        if set(body) - cls.__dataclass_fields__.keys():
            raise BadRequest("unknown activation fields")
        selection = cls(**body)
        for name in ("model", "runtime", "device"):
            value = getattr(selection, name)
            if not isinstance(value, str) or not value.strip():
                raise BadRequest(f"{name} must be a non-empty string")
        for name in ("path", "revision"):
            value = getattr(selection, name)
            if value is not None and (not isinstance(value, str) or not value.strip()):
                raise BadRequest(f"{name} must be a non-empty string or null")
        if selection.runtime not in ("laya", "system_one"):
            raise BadRequest("runtime must be laya or system_one (native typed choice)")
        validate_device(selection.device)
        if type(selection.trust_remote_code) is not bool:
            raise BadRequest("trust_remote_code must be a boolean")
        return selection

    def describe(self):
        return asdict(self)


def validate_device(device):
    if device == "npu":
        raise BadRequest('device "npu" retired (implementation parked under future/npu): want auto, cpu, or gpu')
    if device not in ("auto", "cpu", "gpu"):
        raise BadRequest(f"unknown device {device!r}: want auto, cpu, or gpu")


def resolve(device):
    validate_device(device)
    if device == "cpu":
        return "torch", "cpu"
    import torch
    if torch.cuda.is_available():
        return "torch", "cuda"
    mps = getattr(torch.backends, "mps", None)
    if mps is not None and mps.is_available():
        return "torch", "mps"
    if device == "gpu":
        raise BadRequest("device gpu asked but torch reports no GPU here")
    return "torch", "cpu"


def validate_judge(body, catalog=None):
    if not isinstance(body, dict):
        raise BadRequest("judge body must be a JSON object")
    model = body.get("model")
    if not isinstance(model, str) or not model.strip():
        raise BadRequest("judge needs a non-empty model selector")
    context, options = body.get("context"), body.get("options")
    if not isinstance(context, str) or not context.strip():
        raise BadRequest("judge needs a non-empty context string")
    if not isinstance(options, list) or len(options) < 2:
        raise BadRequest("judge needs at least 2 options")
    if any(not isinstance(o, str) or not o.strip() for o in options):
        raise BadRequest("judge options must be non-empty strings")
    if len(set(options)) != len(options):
        raise BadRequest("judge options must be distinct strings")
    instructions = body.get("instructions")
    if instructions is None or instructions == "":
        instructions = (catalog or load_prompts()).text("decision.default.instructions")
    if not isinstance(instructions, str):
        raise BadRequest("judge instructions must be a string")
    validate_device(body.get("device", "auto"))
    return context, options, instructions


def probability(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not 0 <= value <= 1:
        raise RuntimeError(f"native model returned invalid {name}: {value!r}")
    return float(value)


def typed_answer(raw, options, runtime):
    try:
        answer = raw["answers"]["pick"]
        if answer["type"] != "choice" or answer["choice"] not in options:
            raise RuntimeError("native model returned an unknown choice/type")
        label = answer["choice"]
        confidence = probability(answer["confidence" if runtime == "system_one" else "answer_confidence"], "confidence")
        probs = answer.get("probabilities")
        if probs is not None or runtime == "system_one":
            if not isinstance(probs, dict) or set(probs) != set(options):
                raise RuntimeError("native probability labels do not match options")
            probs = {k: probability(v, "probability") for k, v in probs.items()}
            # Laya rounds each native probability to four decimal places.
            # Refuse vectors whose rounding cannot establish mass within 0.1%.
            tolerance = min(0.001, max(1e-5, len(options) * 0.00005)) if runtime == "laya" else 1e-5
            if abs(sum(probs.values()) - 1) > tolerance + 1e-9:
                raise RuntimeError(f"native probabilities do not sum to 1 within {tolerance:g}; rounded distribution cannot be validated")
            if probs[label] + 1e-6 < max(probs.values()):
                raise RuntimeError("native choice is not a maximum-probability option")
            if runtime == "system_one" and abs(confidence - probs[label]) > 1e-6:
                raise RuntimeError("native confidence/choice disagrees with probabilities")
        if runtime == "system_one":
            usage = raw["usage"]
            if type(usage.get("input_tokens")) is not int or usage["input_tokens"] <= 0 or type(usage.get("output_tokens")) is not int or usage["output_tokens"] != 0:
                raise RuntimeError("native model returned invalid single-forward token usage")
        return label, confidence
    except (KeyError, TypeError) as error:
        raise RuntimeError(f"invalid native typed-choice response: {error}") from error


def assets(selection):
    """Resolve only local assets; library aliases are convenience, not policy."""
    source, subfolder = selection.path or selection.model, None
    local = selection.path is not None or source.startswith(("/", ".", "~")) or Path(source).is_dir()
    if local:
        directory = Path(source).expanduser().resolve()
        if not directory.is_dir():
            raise BadRequest(f"missing local checkpoint: {directory}")
    else:
        if selection.runtime == "laya":
            from laya.router import resolve_model_spec
            spec = resolve_model_spec(source)
            if spec is not None:
                source, subfolder = spec
        from huggingface_hub import snapshot_download
        try:
            directory = Path(snapshot_download(source, revision=selection.revision, local_files_only=True)).resolve()
        except Exception as error:
            raise BadRequest(f"checkpoint {source!r} is not downloaded at requested revision; download its complete assets first: {error}") from error
    revision = next((p.name for p in (directory, *directory.parents) if p.parent.name == "snapshots"), None)
    if local and selection.revision is not None and selection.revision != revision:
        raise BadRequest("local checkpoint revision cannot be verified; use its exact cached snapshot revision or omit revision")
    if subfolder:
        directory = directory / subfolder
    if not directory.is_dir():
        raise BadRequest(f"missing local checkpoint: {directory}")
    return directory, source, revision


def model_identity(model, target):
    parameters = list(model.parameters())
    if not parameters:
        raise RuntimeError("loaded model has no parameters")
    devices = {str(p.device) for p in parameters}
    if len(devices) != 1:
        raise RuntimeError(f"unsupported multi-device checkpoint: actual {sorted(devices)}")
    if any(d.split(":")[0] != target for d in devices):
        raise RuntimeError(f"loaded device mismatch: requested {target}, actual {sorted(devices)}")
    return {"native_device": next(iter(devices)),
            "device": "cpu" if target == "cpu" else "gpu",
            "parameters": sum(p.numel() for p in parameters)}


def laya_input(model, context, question):
    """Pinned Laya0.3.28 native formatting; refuse rather than score an excerpt."""
    from laya.common import build_head, encode_text, render_options
    from laya.hooks import compose_hooks
    if compose_hooks(model.hooks):
        raise RuntimeError("full-input Laya judgment does not support prediction hooks")
    maximum, head_maximum = model.cfg.get("max_len"), model.cfg.get("head_max_len")
    if any(type(v) is not int or v <= 0 for v in (maximum, head_maximum)):
        raise RuntimeError("Laya checkpoint lacks verified max_len/head_max_len")
    q, tok = model._to_internal(question), model.tok
    def tokens(text):
        return encode_text(tok, text.replace(tok.mask_token, " "), add_special_tokens=False)["input_ids"]
    full_head = [tok.cls_token_id] + tokens(q["t"] + " question: " + q["ins"]) + [tok.sep_token_id]
    for option in render_options(q):
        full_head += [tok.mask_token_id] + tokens(" " + option)
    full_head.append(tok.sep_token_id)
    head, _, _ = build_head(tok, q, head_maximum)
    if head != full_head:
        raise BadRequest("Laya question/options do not fit the selected head budget; shorten instructions/options")
    item = model._encode_state(context, ["pick"], {"pick": q})[0]
    if item["state_stats"]["state_tokens"] <= 0 or item["state_stats"]["state_tokens_dropped"] or len(item["ids"]) > maximum:
        raise BadRequest("complete context does not fit the selected Laya model; shorten context/options")
    return {"input_tokens": len(item["ids"]), "output_tokens": 0,
            "state_tokens": item["state_stats"]["state_tokens"], "state_tokens_dropped": 0, "truncated": False}


def complete_usage(raw, maximum, expected=None):
    usage = raw.get("usage") if isinstance(raw, dict) else None
    if not isinstance(usage, dict) or type(usage.get("input_tokens")) is not int or not 0 < usage["input_tokens"] <= maximum or type(usage.get("output_tokens")) is not int or usage["output_tokens"] != 0:
        raise RuntimeError("native judgment lacks valid in-cap single-forward usage")
    if ("truncated" in usage and (type(usage["truncated"]) is not bool or usage["truncated"])) or ("state_tokens_dropped" in usage and (type(usage["state_tokens_dropped"]) is not int or usage["state_tokens_dropped"] != 0)):
        raise RuntimeError("native judgment truncated input; no complete-input verdict available")
    if expected is not None and any(type(usage.get(k)) is not type(v) or usage.get(k) != v for k, v in expected.items()):
        raise RuntimeError("Laya native usage does not match the admitted complete input")


MAX_JUDGMENT_TOKENS = 15999  # Complete native input, including instructions and options.


def release_device_cache():
    """Return cached device allocations after one judgment.

    A long-lived sidecar otherwise grows resident memory across thousands
    of small judgments (observed at 158 GB after ~22h), starving co-resident
    model servers on unified-memory machines. Maintenance only: a release
    failure must never replace the verdict, so callers log and continue.
    """
    try:
        import torch
    except Exception:
        return
    mps = getattr(torch, "mps", None)
    if mps is not None and mps.is_available():
        mps.synchronize()
        mps.empty_cache()
    cuda = getattr(torch, "cuda", None)
    if cuda is not None and cuda.is_available():
        cuda.synchronize()
        cuda.empty_cache()


class Backend:
    def __init__(self, selection, model, identity, choice=None):
        self.selection, self.model, self.identity, self.choice = selection, model, identity, choice

    def judge(self, context, options, instructions, max_input_tokens=MAX_JUDGMENT_TOKENS):
        question = {"pick": {"type": "choice", "instructions": instructions, "criteria": {o: o for o in options}}}
        expected = None
        if type(max_input_tokens) is not int or max_input_tokens <= 0:
            raise BadRequest("max_input_tokens must be a positive integer")
        maximum = min(self.identity["context_limit"], max_input_tokens)
        limit_name = "decision input" if maximum < self.identity["context_limit"] else "model context"
        if self.selection.runtime == "system_one":
            count = self.model.engine.tokens(context, [self.choice(instructions, {o: o for o in options})])
            if type(count) is not int or count <= 0:
                raise RuntimeError("native tokenizer returned an invalid token count")
            if count > maximum:
                raise BadRequest(f"request has {count} tokens; {limit_name} limit is {maximum}; shorten context/options")
            raw = self.model.system_one(context, question)
        else:
            expected = laya_input(self.model, context, question["pick"])
            if expected["input_tokens"] > maximum:
                raise BadRequest(f"request has {expected['input_tokens']} tokens; {limit_name} limit is {maximum}; shorten context/options")
            before = getattr(self.model, "cpu_fallback_count", 0)
            raw = self.model.predict(context, question)
            if getattr(self.model, "cpu_fallback_count", 0) != before:
                raise RuntimeError("Laya used CPU fallback; judgment refused rather than reporting the loaded GPU")
        complete_usage(raw, maximum, expected)
        weights = self.model.model if self.selection.runtime == "laya" else self.model
        model_identity(weights, self.identity["native_device"].split(":")[0])
        label, confidence = typed_answer(raw, options, self.selection.runtime)
        return {"pick": options.index(label), "option": label, "confidence": confidence,
                "model": self.selection.model, "device": self.identity["device"]}


def load_backend(selection):
    directory, source, revision = assets(selection)
    _, target = resolve(selection.device)
    identity = {"model": selection.model, "runtime": selection.runtime, "source": source,
                "path": str(directory), "revision": revision}
    choice = None
    if selection.runtime == "laya":
        for name in ("rl_agent_config.json", "model.safetensors", "tokenizer"):
            if not (directory / name).exists():
                raise BadRequest(f"incompatible/incomplete Laya checkpoint: missing {name}")
        from laya import load
        model = load(str(directory), device=target)
        if not callable(getattr(model, "predict", None)):
            raise RuntimeError("checkpoint lacks native Laya predict")
        weights = model.model
        identity["context_limit"] = model.cfg.get("max_len")
    else:
        import torch
        from transformers import AutoModel
        model, loading = AutoModel.from_pretrained(str(directory), trust_remote_code=selection.trust_remote_code,
                local_files_only=True, dtype=torch.float32 if target == "cpu" else torch.bfloat16, output_loading_info=True)
        if any(loading.get(k) for k in ("missing_keys", "unexpected_keys", "mismatched_keys", "error_msgs")):
            raise RuntimeError(f"checkpoint did not load exactly: {loading}")
        if not callable(getattr(model, "system_one", None)):
            raise BadRequest("incompatible checkpoint: runtime system_one requires native system_one; generic chat models cannot supply decision confidence")
        # Lazy engines capture the current model device on first access.
        model = model.to(target).eval()
        if not callable(getattr(getattr(model, "engine", None), "tokens", None)):
            raise BadRequest("incompatible checkpoint: runtime system_one requires native system_one and engine.tokens; generic chat models cannot supply decision confidence")
        try:
            choice = importlib.import_module(model.__class__.__module__.rsplit(".", 1)[0] + ".prompt").Choice
        except (ImportError, AttributeError) as error:
            raise BadRequest("incompatible System One checkpoint: native prompt.Choice factory is unavailable") from error
        config = getattr(model.config, "text_config", model.config)
        identity["context_limit"] = config.get("max_position_embeddings") if isinstance(config, dict) else getattr(config, "max_position_embeddings", None)
        if type(identity["context_limit"]) is not int or identity["context_limit"] <= 0:
            raise BadRequest("native System One checkpoint has no verified context limit")
        if target == "mps":
            from mps_attention import install
            identity["mps_chunked_attention"] = install(model)
        weights = model
    identity.update(model_identity(weights, target))
    identity["architecture"] = type(model).__name__
    return Backend(selection, model, identity, choice)
