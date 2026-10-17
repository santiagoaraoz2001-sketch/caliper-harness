#!/usr/bin/env python3
"""Offline native decision sidecar: Laya or System One compatible checkpoints.

Install requirements.txt (or requirements-system-one.txt), download assets, then:
  python server.py --model typed-decisions --device auto
  python server.py --runtime system_one --model org/checkpoint --trust-remote-code
GET /health reports requested and actually loaded identity. POST /activate accepts
model/runtime/device/path/revision/trust_remote_code and preserves the active model
on failure. POST /judge retains the harness typed-choice wire contract. Readiness
means a validated native forward executed; it does not establish decision quality.
"""
import argparse
import json
import os
import sys
import threading
import time
import math
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from backends import BadRequest, Selection, load_backend, release_device_cache, resolve, validate_judge
from prompts import load as load_prompts

MODEL = "typed-decisions"


class JudgmentBusy(Exception):
    pass


class JudgmentExpired(Exception):
    pass


def check_deadline(body):
    deadline = body.get("deadline_unix_ms")
    if deadline is None:
        return
    if type(deadline) not in (int, float) or not math.isfinite(deadline) or deadline <= 0:
        raise BadRequest("deadline_unix_ms must be a positive finite timestamp")
    if time.time() * 1000 >= deadline:
        raise JudgmentExpired("judgment deadline expired; no verdict available")


class Manager:
    def __init__(self, loader=load_backend, catalog=None):
        self.loader = loader
        self.prompts = catalog or load_prompts()
        self.active = None
        self.requested = None
        self.loading = False
        self.error = None
        self.state_lock = threading.Lock()
        self.judgment_lock = threading.Lock()
        self.activation_lock = threading.Lock()

    def health(self):
        with self.state_lock:
            active = dict(self.active.identity) if self.active else None
            if active:
                active["selection"] = self.active.selection.describe()
            return {"pid": os.getpid(), "status": "ok" if active else "not_ready", "loaded": bool(active),
                    "ready": bool(active), "model": active["model"] if active else None,
                    "active": active, "requested": self.requested, "loading": self.loading,
                    "error": self.error, "backends": [active["device"]] if active else []}

    def activate(self, selection):
        if not self.activation_lock.acquire(blocking=False):
            raise BadRequest("another activation is in progress; active model is unchanged")
        try:
            with self.state_lock:
                self.requested, self.loading, self.error = selection.describe(), True, None
            candidate = self.loader(selection)
            # Real candidate forward, never a confidence/accuracy qualification.
            candidate.judge(self.prompts.text("decision.readiness_probe.context"), ["four", "five"], self.prompts.text("decision.readiness_probe.instructions"))
            with self.judgment_lock:  # drain old judgments before swapping
                with self.state_lock:
                    self.active, self.loading = candidate, False
            return self.health()
        except Exception as error:
            with self.state_lock:
                self.loading, self.error = False, f"{type(error).__name__}: {error}"
            raise
        finally:
            self.activation_lock.release()

    def judge(self, body):
        context, options, instructions = validate_judge(body, self.prompts)
        check_deadline(body)
        if not self.judgment_lock.acquire(blocking=False):
            raise JudgmentBusy("decision model is busy with an active judgment; request was not queued")
        try:
            check_deadline(body)
            with self.state_lock:
                backend = self.active
            if backend is None:
                raise BadRequest("no ready model; activate a compatible downloaded checkpoint first")
            if body["model"] != backend.selection.model:
                raise BadRequest(f"this sidecar serves {backend.selection.model!r}, asked for {body['model']!r}")
            if body.get("expected") is not None:
                expected = Selection.from_body(body["expected"])
                wanted, admitted = expected.describe(), backend.selection.describe()
                wanted.pop("device")
                admitted.pop("device")
                if wanted != admitted or expected.device != "auto" and expected.device != backend.identity["device"]:
                    raise BadRequest("requested decision asset binding is pending activation")
            device = body.get("device", "auto")
            if device != "auto" and device != backend.identity["device"]:
                raise BadRequest(f"active model device is {backend.identity['device']}; activate the requested device first")
            # Native forwards cannot safely be interrupted mid-kernel. Do not queue
            # followers, and never return a verdict after its caller budget expired.
            check_deadline(body)
            limits = {}
            if "max_input_tokens" in body:
                limit = body["max_input_tokens"]
                if type(limit) is not int or limit <= 0:
                    raise BadRequest("max_input_tokens must be a positive integer")
                limits["max_input_tokens"] = limit
            result = backend.judge(context, options, instructions, **limits)
            check_deadline(body)
            return result
        finally:
            try:
                release_device_cache()
            except Exception:
                pass  # maintenance only; a released-or-not verdict stands
            self.judgment_lock.release()


MANAGER = None  # main loads the selected catalog before constructing the owner.


def judge(body):
    if MANAGER is None:
        raise BadRequest("no ready model; activate a compatible downloaded checkpoint first")
    return MANAGER.judge(body)


class Handler(BaseHTTPRequestHandler):
    server_version = "decision-sidecar/2"

    def _send(self, status, obj):
        data = json.dumps(obj, allow_nan=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path != "/health":
            return self._send(404, {"error": "want /health, /judge or /activate"})
        self._send(200, MANAGER.health())

    def do_POST(self):
        if self.path not in ("/judge", "/activate"):
            return self._send(404, {"error": "want /health, /judge or /activate"})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 1048576:
                raise BadRequest("body length must be 1..1048576 bytes")
            data = self.rfile.read(length)
            if len(data) != length:
                raise BadRequest("incomplete request body")
            body = json.loads(data)
            result = MANAGER.activate(Selection.from_body(body)) if self.path == "/activate" else judge(body)
            self._send(200, result)
        except JudgmentBusy as error:
            self._send(503, {"error": str(error)})
        except JudgmentExpired as error:
            self._send(408, {"error": str(error)})
        except (BadRequest, ValueError) as error:
            self._send(400, {"error": str(error)})
        except Exception as error:
            self._send(500, {"error": f"decision failed: {type(error).__name__}: {error}"})

    def log_message(self, *args):
        pass


def main():
    global MANAGER
    ap = argparse.ArgumentParser(description="Native typed-choice decision sidecar; downloaded compatible checkpoints only")
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8090)
    ap.add_argument("--model", default=MODEL)
    ap.add_argument("--runtime", default="laya", choices=("laya", "system_one"))
    ap.add_argument("--device", default="auto")
    ap.add_argument("--model-path", dest="path")
    ap.add_argument("--revision")
    ap.add_argument("--trust-remote-code", action="store_true")
    ap.add_argument("--prompts-file", help="shared prompts.yaml catalog; defaults to the shipped application root")
    args = ap.parse_args()
    # Owned process only. Native loaders must never fetch missing dependencies/assets.
    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    os.environ["PYTORCH_ENABLE_MPS_FALLBACK"] = "0"
    try:
        MANAGER = Manager(catalog=load_prompts(args.prompts_file))
        selection = Selection.from_body({k: v for k, v in vars(args).items() if k not in ("host", "port", "prompts_file")})
        health = MANAGER.activate(selection)
    except Exception as error:
        print(f"error: activation failed: {type(error).__name__}: {error}", file=sys.stderr)
        if isinstance(error, ImportError):
            requirements = "requirements-system-one.txt" if args.runtime == "system_one" else "requirements.txt"
            print(f"install {requirements} in this server's Python environment", file=sys.stderr)
        return 1
    print(json.dumps({"listening": f"{args.host}:{args.port}", "health": health}, allow_nan=False), flush=True)
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main())
