"""Fixed-token controls using the exact pinned native parser/formatter/handler source.

No learned model, native provider, generation thread, MLX import, or GPU allocation.
"""
import abc
import ast
import copy
from collections import deque
from dataclasses import dataclass
import hashlib
import io
import json
import logging
import os
from pathlib import Path
import types
import unittest
import uuid

try:
    from continuation import install_continuation_hooks
except ModuleNotFoundError:
    # This permits the same behavioral oracle to run against the frozen baseline.
    def install_continuation_hooks(native):
        pass

ROOT = Path(__file__).resolve().parents[2]
PINNED = Path(os.environ.get("SPECIFIC_HARNESS_PINNED_SERVER", str(ROOT / "verification/2026-10-09-remediation/R34/optional-guided-planning-20261012/native-tests/laguna-deterministic-runtime/upstream-mlx_lm-server.py")))
LIB = Path("/Users/santiagoaraoz/Library/Python/3.12/lib/python/site-packages/mlx_lm")


def definitions(path, names, namespace, owner=None):
    tree = ast.parse(path.read_text())
    body = tree.body
    if owner:
        body = next(n.body for n in body if isinstance(n, ast.ClassDef) and n.name == owner)
    selected = [n for n in body if isinstance(n, (ast.ClassDef, ast.FunctionDef)) and n.name in names]
    if len(selected) != len(names):
        raise AssertionError("fixed native source definitions drifted")
    code = ast.Module(body=[ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0), *selected], type_ignores=[])
    exec(compile(ast.fix_missing_locations(code), str(path), "exec"), namespace)


def native_cache_fixture():
    namespace = dict(copy=copy, deque=deque, dataclass=dataclass)
    definitions(LIB / "models/cache.py", {"_BaseCache", "KVCache", "RotatingKVCache", "PromptTrieResult", "PromptTrie", "LRUPromptCache", "can_trim_prompt_cache", "trim_prompt_cache"}, namespace)
    return namespace


def native_fixture(text, cutoffs, parser=None):
    ns = dict(abc=abc, deque=deque, dataclass=dataclass, json=json, logging=logging, uuid=uuid)
    definitions(LIB / "generate.py", {"_build_trie", "TextStateMachine"}, ns)
    definitions(LIB / "tokenizer_utils.py", {"StreamingDetokenizer", "NaiveStreamingDetokenizer"}, ns)
    definitions(PINNED, {"ToolCallFormatter", "process_message_content", "convert_chat", "CompletionRequest", "ModelDescription", "SamplingArguments", "LogitsProcessorArguments", "GenerationArguments", "GenerationContext", "Response"}, ns)
    definitions(PINNED, {"_tokenize"}, ns, "ResponseGenerator")
    tokenize = ns.pop("_tokenize")
    definitions(PINNED, {"handle_chat_completions", "handle_completion", "generate_response", "completion_usage_response"}, ns, "APIHandler")
    raw = text.encode()
    token_bytes = {i+1: bytes([b]) for i, b in enumerate(raw)}
    token_bytes[0] = b"a ,b"

    class Tokenizer:
        has_chat_template = has_tool_calling = has_thinking = True
        def apply_chat_template(self, messages, **kwargs):
            return [500]
        def rfind_think_start(self, prompt, **kwargs):
            return 0
        def rfind_think_end(self, prompt):
            return -1
        def encode(self, text, **kwargs):
            return [0]
        def decode(self, tokens):
            return b"".join(token_bytes[t] for t in tokens).decode(errors="replace")
        @property
        def detokenizer(self):
            return ns["NaiveStreamingDetokenizer"](self)
        def tool_parser(self, text, tools):
            return json.loads(text)

    class Generator:
        _tokenize = tokenize
        def __init__(self):
            self.model_provider = types.SimpleNamespace(tokenizer=Tokenizer(), cli_args=types.SimpleNamespace(chat_template_args={}))
            self.prompt_cache = native_cache_fixture()["LRUPromptCache"]()
            self.contexts = []
        def generate(self, request, args, progress_callback=None):
            prompt, _, _, initial = self._tokenize(self.model_provider.tokenizer, request, args)
            sm = ns["TextStateMachine"]({"normal": [("<tool_call>", "tool"), ("<think>", "reasoning")], "reasoning": [("</think>", "normal"), ("<tool_call>", "tool")], "tool": [("</tool_call>", "normal")]})
            ctx = ns["GenerationContext"](True, True, self.model_provider.tokenizer.tool_parser, sm, initial, prompt)
            self.contexts.append(ctx)
            begin = len(prompt)-1
            end = next((cut for cut in cutoffs if cut > begin), len(raw))
            def stream():
                detok = self.model_provider.tokenizer.detokenizer
                for i in range(begin, end):
                    token = i+1
                    detok.add_token(token)
                    finish = "length" if i+1 == end and end < len(raw) else None
                    if finish:
                        detok.finalize()
                    yield ns["Response"](detok.last_segment, token, 0., finish, [])
                if end == len(raw):
                    detok.finalize()
                    yield ns["Response"](detok.last_segment, 999, 0., "stop", [])
            return ctx, stream()

    if parser is not None:
        Tokenizer.tool_parser = staticmethod(parser)

    class Handler:
        handle_chat_completions = ns["handle_chat_completions"]
        handle_completion = ns["handle_completion"]
        generate_response = ns["generate_response"]
        completion_usage_response = ns["completion_usage_response"]
        def _set_completion_headers(self, code): self.status = code
        def _set_stream_headers(self, code): self.status = code
        def end_headers(self): pass

    native = types.SimpleNamespace(**{name: ns[name] for name in ("TextStateMachine", "ToolCallFormatter", "GenerationArguments", "ModelDescription", "SamplingArguments", "LogitsProcessorArguments")}, ResponseGenerator=Generator, APIHandler=Handler, logging=logging)
    return native


def handler(native, generator, prefix=None, messages=None):
    h = native.APIHandler()
    h.response_generator, h.wfile = generator, io.BytesIO()
    h.body = {"messages": messages or [{"role":"user","content":"original objective"}], "tools":[{"type":"function"}], "response_prefix":prefix}
    h.stream, h.stream_options = True, {"include_usage":True}
    h.requested_model, h.requested_draft_model, h.adapter = "fixture", "default_model", None
    for name in ("temperature", "top_p", "top_k", "min_p", "xtc_probability", "xtc_threshold", "repetition_penalty", "repetition_context_size", "presence_penalty", "presence_context_size", "frequency_penalty", "frequency_context_size", "max_tokens", "num_draft_tokens", "top_logprobs"):
        setattr(h,name,1)
    h.logit_bias, h.seed, h.chat_template_kwargs, h.logprobs = None, None, None, False
    h.system_fingerprint, h.created = "fixed-native-control", 0
    return h


def rows(h):
    return [json.loads(line[6:]) for line in h.wfile.getvalue().decode().splitlines() if line.startswith("data: ") and line != "data: [DONE]"]


class LiteralContinuationControls(unittest.TestCase):
    def zero_argument_tools(self):
        return [{"type":"function", "function":{"name":"plan", "parameters":{
            "type":"object", "properties":{}, "required":[], "additionalProperties":False}}}]

    def actual_glm_parser(self):
        namespace = {"__name__":"mlx_lm.tool_parsers.glm47"}
        path = LIB / "tool_parsers" / "glm47.py"
        exec(compile(path.read_text(),str(path),"exec"),namespace)
        return namespace["parse_tool_call"]

    def test_length_crosses_thought_marker_unicode_and_native_array_once(self):
        text = 'α exact thought</think><tool_call>[{"name":"effect","arguments":{"name":"a🙂b"}},{"name":"effect","arguments":{"name":"second"}}]</tool_call>final answer'
        raw = text.encode()
        cutoffs = [raw.index(b"</think>")+4, raw.index("🙂".encode())+2]
        native = native_fixture(text,cutoffs)
        install_continuation_hooks(native)
        generator = native.ResponseGenerator()
        prefix, calls, thinking, answer = None, [], "", ""
        for attempt in range(3):
            h = handler(native,generator,prefix)
            request = h.handle_chat_completions()
            h.handle_completion(request,[])
            data = rows(h)
            self.assertEqual(h.status,200)
            terminal = [row for row in data if row.get("choices") and row["choices"][0]["finish_reason"]][-1]
            for row in data:
                for choice in row["choices"]:
                    delta = choice["delta"]
                    thinking += delta.get("reasoning","")
                    answer += delta.get("content","")
                    calls += delta.get("tool_calls",[])
            if attempt < 2:
                self.assertEqual(terminal["choices"][0]["finish_reason"],"length")
                self.assertIn("response_prefix",terminal,"baseline loses the unfinished parser/token prefix")
                prefix = terminal["response_prefix"]
                self.assertEqual(prefix["tokens"],list(range(1,cutoffs[attempt]+1)))
                self.assertEqual(generator.contexts[-1].prompt,[500]+list(range(1,(cutoffs[attempt-1] if attempt else 0)+1)))
                self.assertEqual(calls,[],"a partial native array became executable")
            else:
                self.assertEqual(terminal["choices"][0]["finish_reason"],"tool_calls")
        self.assertEqual(thinking,'α exact thought')
        self.assertEqual(answer,'final answer')
        self.assertEqual([c["index"] for c in calls],[0,1])
        self.assertEqual([json.loads(c["function"]["arguments"])["name"] for c in calls],["a🙂b","second"])
        self.assertEqual(len({c["id"] for c in calls}),2)
        self.assertTrue(all(ctx._should_stop for ctx in generator.contexts))

    def test_prefix_with_changed_original_context_is_refused(self):
        native = native_fixture("unfinished thought",[4]); install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native,generator)
        h.handle_completion(h.handle_chat_completions(),[])
        first = [r for r in rows(h) if "response_prefix" in r]
        self.assertTrue(first,"baseline returned no literal prefix")
        prefix = first[0]["response_prefix"]; prefix["prompt_sha256"] = "0"*64
        second = handler(native,generator,prefix)
        second.handle_completion(second.handle_chat_completions(),[])
        self.assertEqual(second.status,400)
        self.assertIn("unchanged original",json.loads(second.wfile.getvalue())["error"])

    def test_pending_native_json_never_silently_disappears_at_normal_stop(self):
        draft = '[{"name":"effect","arguments":'
        native = native_fixture('</think><tool_call>' + draft,[])
        install_continuation_hooks(native); generator = native.ResponseGenerator(); h = handler(native,generator)
        with self.assertLogs(level="ERROR"):
            h.handle_completion(h.handle_chat_completions(),[])
        self.assertEqual(rows(h)[-1]["error"]["native_tool_draft"], draft)
        self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
        self.assertNotIn(b'"finish_reason": "tool_calls"',h.wfile.getvalue())
        self.assertTrue(generator.contexts[-1]._should_stop)

    def test_captured_failures_expose_settled_recovery_boundary(self):
        drafts = [
            'plan><arg_key></arg_key><arg_value>{}</arg_value>',
            'plan<arg_key>argument_keys></arg_key><arg_value>{}</arg_value><arg_key>argument_values>',
        ]
        for draft in drafts:
            for close, boundary in [('</tool_call>', 'tool_close'), ('', 'stop')]:
                with self.subTest(draft=draft, boundary=boundary):
                    native = native_fixture('</think><tool_call>' + draft + close, [], self.actual_glm_parser())
                    install_continuation_hooks(native)
                    generator = native.ResponseGenerator(); h = handler(native, generator)
                    with self.assertLogs(level='ERROR'):
                        h.handle_completion(h.handle_chat_completions(), [])
                    detail = rows(h)[-1]['error']
                    self.assertEqual(detail['native_tool_failure'], dict(
                        draft=draft, reason=detail['message'], boundary=boundary, call_index=0))
                    self.assertTrue(generator.contexts[-1]._should_stop)

    def test_length_on_failed_tool_close_does_not_enable_recovery(self):
        text = '</think><tool_call>plan><arg_key></arg_key><arg_value>{}</arg_value></tool_call>'
        native = native_fixture(text + 'tail', [len(text)], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator)
        with self.assertLogs(level='ERROR'):
            h.handle_completion(h.handle_chat_completions(), [])
        self.assertNotIn('native_tool_failure', rows(h)[-1]['error'])

    def test_actual_laguna_parser_preserves_all_json_array_members(self):
        text = '</think><tool_call>[{"name":"effect","arguments":{"name":"first"}},{"name":"effect","arguments":{"name":"second"}}]</tool_call>'
        native = native_fixture(text,[],self.actual_glm_parser()); install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native,generator)
        h.handle_completion(h.handle_chat_completions(),[])
        calls = [call for row in rows(h) for choice in row["choices"] for call in choice["delta"].get("tool_calls",[])]
        self.assertEqual([json.loads(call["function"]["arguments"])["name"] for call in calls],["first","second"])
        self.assertEqual([call["index"] for call in calls],[0,1])

    def test_actual_laguna_parser_refuses_unfinished_optional_argument(self):
        draft = 'effect<arg_key>name</arg_key><arg_value>complete</arg_value><arg_key>optional</arg_key><arg_value>unfinished'
        text = '</think><tool_call>' + draft
        native = native_fixture(text,[],self.actual_glm_parser()); install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native,generator)
        with self.assertLogs(level="ERROR"):
            h.handle_completion(h.handle_chat_completions(),[])
        error = rows(h)[-1]["error"]
        self.assertIn("unfinished native GLM argument", error["message"])
        self.assertEqual(error["native_tool_draft"], draft)
        self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
        self.assertNotIn(b'"tool_calls":',h.wfile.getvalue())
        self.assertTrue(generator.contexts[-1]._should_stop)

    def test_actual_laguna_duplicate_keys_refuse_with_exact_generated_draft(self):
        draft = 'effect<arg_key>name</arg_key><arg_value>first</arg_value><arg_key>name</arg_key><arg_value>second</arg_value>'
        native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator)
        with self.assertLogs(level="ERROR") as log:
            h.handle_completion(h.handle_chat_completions(), [])
        data = rows(h)
        self.assertEqual(data[0]["error"]["native_tool_draft"], draft)
        self.assertEqual(data[0]["error"]["native_tool_failure"]["boundary"], "tool_close")
        self.assertIn(repr(draft), log.output[0])
        self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
        self.assertTrue(generator.contexts[-1]._should_stop)

    def test_native_json_duplicate_argument_does_not_become_last_value(self):
        draft = '{"name":"effect","arguments":{"name":"first","name":"second"}}'
        native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator)
        with self.assertLogs(level="ERROR"):
            h.handle_completion(h.handle_chat_completions(), [])
        self.assertEqual(rows(h)[0]["error"]["native_tool_draft"], draft)
        self.assertEqual(rows(h)[0]["error"]["native_tool_failure"]["reason"], "duplicate native JSON argument/key")
        self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
        self.assertTrue(generator.contexts[-1]._should_stop)

    def test_actual_captured_laguna_empty_argument_framing_matches_zero_schema(self):
        draft = 'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value>'
        self.assertEqual(hashlib.sha256(draft.encode()).hexdigest(),
                         '359c9873e0e0b4c7b3e2e60f57c1a08f27dc511ecf0665f144cf2f2888618193')
        native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator)
        h.body['tools'] = self.zero_argument_tools()
        h.handle_completion(h.handle_chat_completions(), [])
        calls = [c for row in rows(h) for choice in row.get('choices', []) for c in choice['delta'].get('tool_calls', [])]
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]['function'], {'name':'plan', 'arguments':'{}'})
        self.assertEqual(calls[0]['index'], 0)
        self.assertEqual(rows(h)[-2]['choices'][0]['finish_reason'], 'tool_calls')
        self.assertIn(b'data: [DONE]', h.wfile.getvalue())
        self.assertTrue(generator.contexts[-1]._should_stop)

    def test_empty_argument_recovery_refuses_unknown_or_argument_schemas(self):
        draft = 'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value>'
        original = self.zero_argument_tools()
        schemas = [None, [], original * 2]
        for field, value in [('type', 'string'), ('properties', {'value':{'type':'string'}}),
                             ('required', ['value']), ('additionalProperties', True)]:
            changed = copy.deepcopy(original)
            changed[0]['function']['parameters'][field] = value
            schemas.append(changed)
        missing = copy.deepcopy(original); del missing[0]['function']['parameters']['properties']; schemas.append(missing)
        extra = copy.deepcopy(original); extra[0]['function']['parameters']['oneOf'] = [{}]; schemas.append(extra)
        for tools in schemas:
            with self.subTest(tools=tools):
                native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
                install_continuation_hooks(native)
                generator = native.ResponseGenerator(); h = handler(native, generator); h.body['tools'] = tools
                with self.assertLogs(level='ERROR'):
                    h.handle_completion(h.handle_chat_completions(), [])
                self.assertEqual(rows(h)[-1]['error']['native_tool_draft'], draft)
                self.assertNotIn(b'"tool_calls":', h.wfile.getvalue())
                self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
                self.assertTrue(generator.contexts[-1]._should_stop)

    def test_empty_argument_recovery_never_discards_named_or_valueful_data(self):
        for draft in [
            'unknown<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value>',
            'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{"value":1}</arg_value>',
            'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{"value":" a🙂\nbytes "}</arg_value>',
            'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value><arg_value>{}</arg_value>',
            'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value>partial',
            'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}',
        ]:
            with self.subTest(draft=draft):
                native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
                install_continuation_hooks(native)
                generator = native.ResponseGenerator(); h = handler(native, generator); h.body['tools'] = self.zero_argument_tools()
                with self.assertLogs(level='ERROR'):
                    h.handle_completion(h.handle_chat_completions(), [])
                self.assertEqual(rows(h)[-1]['error']['native_tool_draft'], draft)
                self.assertNotIn(b'"tool_calls":', h.wfile.getvalue())
                self.assertNotIn(b'data: [DONE]', h.wfile.getvalue())
                self.assertTrue(generator.contexts[-1]._should_stop)

    def test_unpaired_empty_argument_key_is_framing_noise_not_stream_death(self):
        # Captured 2026-10-15 attempt 3: an empty arg_key tag with no paired
        # value carries no data; refusing the whole stream over it killed an
        # otherwise healthy run. The named pair that follows still parses.
        draft = 'plan<arg_key></arg_key><arg_key>input</arg_key><arg_value></arg_value>'
        native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator)
        h.body['tools'] = self.zero_argument_tools()
        h.handle_completion(h.handle_chat_completions(), [])
        calls = [c for row in rows(h) for choice in row.get('choices', []) for c in choice['delta'].get('tool_calls', [])]
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]['function']['name'], 'plan')
        self.assertIn(b'data: [DONE]', h.wfile.getvalue())
        # Named data after the noise tag is preserved end to end, not discarded:
        named = 'plan<arg_key></arg_key><arg_key>value</arg_key><arg_value>{}</arg_value>'
        native = native_fixture('</think><tool_call>' + named + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator); h.body['tools'] = self.zero_argument_tools()
        h.handle_completion(h.handle_chat_completions(), [])
        calls = [c for row in rows(h) for choice in row.get('choices', []) for c in choice['delta'].get('tool_calls', [])]
        self.assertEqual(len(calls), 1)
        self.assertIn('value', calls[0]['function']['arguments'])
        # A paired empty key carrying a value is ambiguous data and still refuses.
        valueful = 'plan<arg_key></arg_key><arg_value>x</arg_value>'
        native = native_fixture('</think><tool_call>' + valueful + '</tool_call>', [], self.actual_glm_parser())
        install_continuation_hooks(native)
        generator = native.ResponseGenerator(); h = handler(native, generator); h.body['tools'] = self.zero_argument_tools()
        with self.assertLogs(level='ERROR'):
            h.handle_completion(h.handle_chat_completions(), [])
        self.assertNotIn(b'"tool_calls":', h.wfile.getvalue())

    def test_captured_empty_call_survives_each_literal_length_split_once(self):
        draft = 'plan<arg_key></arg_key><arg_key></arg_key><arg_value>{}</arg_value>'
        text = '</think><tool_call>' + draft + '</tool_call>done'
        begin = len('</think><tool_call>'.encode())
        end = len(('</think><tool_call>' + draft + '</tool_call>').encode())
        for cutoff in range(begin, len(text.encode())):
            with self.subTest(cutoff=cutoff):
                native = native_fixture(text, [cutoff], self.actual_glm_parser()); install_continuation_hooks(native)
                generator = native.ResponseGenerator(); first = handler(native, generator); first.body['tools'] = self.zero_argument_tools()
                first.handle_completion(first.handle_chat_completions(), [])
                terminal = [row for row in rows(first) if 'response_prefix' in row]
                self.assertEqual(len(terminal), 1)
                first_calls = [c for row in rows(first) for choice in row.get('choices', []) for c in choice['delta'].get('tool_calls', [])]
                self.assertEqual(len(first_calls), 0 if cutoff < end else 1)
                self.assertEqual(terminal[0]['response_prefix']['tokens'], list(range(1, cutoff + 1)))
                second = handler(native, generator, terminal[0]['response_prefix']); second.body['tools'] = self.zero_argument_tools()
                second.handle_completion(second.handle_chat_completions(), [])
                calls = first_calls + [c for row in rows(second) for choice in row.get('choices', []) for c in choice['delta'].get('tool_calls', [])]
                self.assertEqual(len(calls), 1)
                self.assertEqual(calls[0]['function'], {'name':'plan', 'arguments':'{}'})
                self.assertEqual(calls[0]['index'], 0)
                self.assertTrue(all(ctx._should_stop for ctx in generator.contexts))

    def test_actual_laguna_argument_prefill_preserves_unicode_and_name(self):
        text = '</think><tool_call>effect<arg_key>name</arg_key><arg_value>a🙂b</arg_value></tool_call>done'
        raw = text.encode(); cutoff = raw.index("🙂".encode())+2
        native = native_fixture(text,[cutoff],self.actual_glm_parser()); install_continuation_hooks(native)
        generator = native.ResponseGenerator(); first = handler(native,generator)
        first.handle_completion(first.handle_chat_completions(),[])
        terminal = [row for row in rows(first) if "response_prefix" in row]
        self.assertTrue(terminal,"baseline lost the actual Laguna native argument prefix")
        second = handler(native,generator,terminal[0]["response_prefix"])
        second.handle_completion(second.handle_chat_completions(),[])
        calls = [call for row in rows(second) for choice in row["choices"] for call in choice["delta"].get("tool_calls",[])]
        self.assertEqual(len(calls),1)
        self.assertEqual(calls[0]["function"]["name"],"effect")
        self.assertEqual(json.loads(calls[0]["function"]["arguments"]),{"name":"a🙂b"})

    def test_exact_cached_prefix_keeps_real_decode_input_and_native_trim_policy(self):
        cache_native = native_cache_fixture()
        for kind in ("kv", "rotating", "full_rotating"):
            with self.subTest(cache=kind):
                native = native_fixture("unfinished thought", [4])
                install_continuation_hooks(native)
                generator = native.ResponseGenerator()
                first = handler(native, generator)
                first.handle_completion(first.handle_chat_completions(), [])
                terminal = [row for row in rows(first) if "response_prefix" in row]
                self.assertTrue(terminal, "baseline lost the literal prefix before its cache lookup")
                second = handler(native, generator, terminal[0]["response_prefix"])
                request = second.handle_chat_completions()
                args = types.SimpleNamespace(chat_template_kwargs={})
                full, segments, _, _ = generator._tokenize(generator.model_provider.tokenizer, request, args)
                if kind == "kv":
                    original = cache_native["KVCache"]()
                else:
                    original = cache_native["RotatingKVCache"](4 if kind == "full_rotating" else 1024)
                    original._idx = len(full)
                original.offset = len(full)
                generator.prompt_cache.insert_cache("fixture", full, [original])
                cached, rest = generator.prompt_cache.fetch_nearest_cache("fixture", full)
                self.assertTrue(rest, "exact native cache hit removed the entire decode prompt")
                cached_count = len(full) - len(rest)
                self.assertEqual(rest, full[cached_count:])
                self.assertEqual(rest[-1], full[-1], "decode used an invented token")
                self.assertEqual(segments, [full], "literal continuation prompt changed")
                if kind == "full_rotating":
                    self.assertIsNone(cached, "untrimmable cache bypassed its existing native fallback")
                    self.assertEqual(rest, full)
                else:
                    self.assertEqual(cached[0].offset, cached_count)
                    self.assertEqual(rest, full[-2:], "native preceding-prefix lookup changed")
                self.assertEqual(original.offset, len(full), "stored native cache was mutated")
                if kind != "kv":
                    self.assertEqual(original._idx, len(full))
                # A new response restores the unchanged ordinary exact-hit policy.
                ordinary = handler(native, generator).handle_chat_completions()
                generator._tokenize(generator.model_provider.tokenizer, ordinary, types.SimpleNamespace(chat_template_kwargs={}))
                _, ordinary_rest = generator.prompt_cache.fetch_nearest_cache("fixture", full)
                self.assertEqual(ordinary_rest, [])


if __name__ == "__main__":
    unittest.main()
