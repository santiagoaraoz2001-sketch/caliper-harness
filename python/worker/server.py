"""Offline owned-native MLX bootstrap; all loading/generation stays first-party."""
import argparse
import json
import os
from pathlib import Path
import sys
from threading import Thread


def install_native_hooks(native, canonical):
    """Version-specific small health/load hooks; unsupported APIs refuse loudly."""
    if not all(hasattr(native, x) for x in ("ModelProvider", "ResponseGenerator", "APIHandler")):
        raise RuntimeError("unsupported mlx_lm.server: native provider/generator/handler required")
    if not all(callable(getattr(native.ResponseGenerator, name, None)) for name in ("generate", "_generate", "join", "stop_and_join")) or not callable(getattr(native.ModelProvider, "load", None)) or not callable(getattr(native.APIHandler, "handle_health_check", None)):
        raise RuntimeError("unsupported mlx_lm.server: native generation lifecycle/load/health methods required")
    original = native.ModelProvider.load

    def load(provider, model_path, adapter_path=None, draft_model_path=None):
        if model_path not in ("default_model", canonical):
            raise ValueError("owned offline worker only accepts its canonical local model")
        if adapter_path is not None or draft_model_path not in (None, "default_model"):
            raise ValueError("owned offline worker does not resolve adapters/draft models")
        if provider.cli_args.adapter_path is not None or provider.cli_args.draft_model is not None:
            raise ValueError("owned offline worker requires an unadapted local model")
        return original(provider, model_path, adapter_path, draft_model_path)

    def health(handler):
        generator = handler.response_generator
        provider = generator.model_provider
        key = provider.model_key
        loaded = provider.model is not None and key is not None and key[0] == canonical
        tokenizer = provider.tokenizer is not None
        thread = getattr(generator, "_generation_thread", None)
        stopped = getattr(generator, "_stop", None)
        if not isinstance(thread, Thread) or type(stopped) is not bool:
            raise RuntimeError("unsupported mlx_lm.server: native Thread/bool stop lifecycle required")
        generation = thread.is_alive() and not stopped
        payload = {"pid": os.getpid(), "path": key[0] if key else None,
                   "loaded": loaded, "tokenizer": tokenizer, "generation": generation,
                   "default_device": str(native.mx.default_device())}
        handler._set_completion_headers(200 if loaded and tokenizer and generation else 503)
        handler.end_headers()
        handler.wfile.write(json.dumps(payload).encode())
        handler.wfile.flush()

    def models(handler):
        # This offline owner accepts exactly one canonical checkpoint. Its catalog
        # must not depend on an unrelated Hugging Face cache existing in HOME.
        # Availability is separate from health's actual loaded/generation facts.
        payload = {"object": "list", "data": [{"id": canonical, "object": "model", "owned_by": "mlx"}]}
        handler._set_completion_headers(200)
        handler.end_headers()
        handler.wfile.write(json.dumps(payload).encode())
        handler.wfile.flush()

    native.APIHandler.handle_models_request = models
    native.ModelProvider.load = load
    native.APIHandler.handle_health_check = health


def main():
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--model", required=True)
    args, remaining = parser.parse_known_args()
    canonical = str(Path(args.model).resolve(strict=True))
    if not Path(canonical).is_dir():
        raise ValueError("owned MLX model must be an existing local directory")
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    import mlx_lm.server as native
    install_native_hooks(native, canonical)
    from continuation import install_continuation_hooks
    install_continuation_hooks(native)
    from framing import install_framing_hooks
    install_framing_hooks(native)
    sys.argv = [sys.argv[0], "--model", canonical, *remaining]
    native.main()


if __name__ == "__main__":
    main()
