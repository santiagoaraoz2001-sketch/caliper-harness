"""Actual selected native parsers and streaming owner, without model weights."""
import importlib.util
import json
import os
from pathlib import Path
from types import SimpleNamespace
import unittest

from continuation import ResponseState, install_continuation_hooks, parsed_native_calls
from test_continuation import native_fixture, handler, rows


class NativeLiterals(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        supplied = os.environ.get("SPECIFIC_HARNESS_PYTHONIC_PARSER")
        if supplied:
            directory = Path(supplied).parent
        else:
            import mlx_lm.tool_parsers
            directory = Path(mlx_lm.tool_parsers.__file__).parent
        cls.parsers = {}
        for name in ("gemma4", "pythonic", "glm47", "qwen3_coder"):
            spec = importlib.util.spec_from_file_location("mlx_lm.tool_parsers." + name, directory / (name + ".py"))
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
            cls.parsers[name] = module
        cls.tools = [{"type": "function", "function": {"name": "write", "parameters": {
            "type": "object", "properties": {"path": {"type": "string"}, "content": {"type": "string"}}}}}]

    def cases(self):
        return [
            ("gemma4", 'call:write{path:<|"|>a.go<|"|>,content:<|"|>call:plan{}<tool_call|>after<|"|>}', "call:plan{}<tool_call|>after"),
            ("pythonic", '[write(path="a.go", content="before<|tool_call_end|>after")]', "before<|tool_call_end|>after"),
            ("pythonic", '[write(path="a.go", content="quote\\" before<|tool_call_end|>after")]', 'quote" before<|tool_call_end|>after'),
            ("pythonic", "[write(path='a.go', content='''before<|tool_call_end|>after''')]", "before<|tool_call_end|>after"),
            ("glm47", 'write<arg_key>path</arg_key><arg_value>a.go</arg_value><arg_key>content</arg_key><arg_value>before</tool_call>after</arg_value>', "before</tool_call>after"),
            ("glm47", '{"name":"write","arguments":{"path":"a.go","content":"before</tool_call>after"}}', "before</tool_call>after"),
            ("qwen3_coder", '<function=write><parameter=path>a.go</parameter><parameter=content>before</tool_call>after</parameter></function>', "before</tool_call>after"),
        ]

    def calls(self, h):
        return [call for row in rows(h) if row.get("choices")
                for call in row["choices"][0]["delta"].get("tool_calls", [])]

    def test_actual_stream_keeps_literal_inside_outer_write_for_all_selected_formats(self):
        for name, payload, content in self.cases():
            with self.subTest(name=name, payload=payload):
                parser = self.parsers[name]
                text = "</think>" + parser.tool_call_start + payload + parser.tool_call_end
                native = native_fixture(text, [], parser.parse_tool_call, (parser.tool_call_start, parser.tool_call_end))
                install_continuation_hooks(native)
                h = handler(native, native.ResponseGenerator())
                h.body["tools"] = self.tools
                h.handle_completion(h.handle_chat_completions(), [])
                calls = self.calls(h)
                self.assertEqual(len(calls), 1)
                self.assertEqual(calls[0]["function"]["name"], "write")
                self.assertEqual(json.loads(calls[0]["function"]["arguments"]), {"path": "a.go", "content": content})
                self.assertNotIn(b'"error":', h.wfile.getvalue())

    def test_chunk_boundaries_and_length_resume_preserve_exact_values(self):
        for name, payload, content in self.cases():
            parser = self.parsers[name]
            text = parser.tool_call_start + payload + parser.tool_call_end
            native = native_fixture("", [], parser.parse_tool_call)
            for size in (1, 3, 11, len(text)):
                with self.subTest(name=name, chunk=size):
                    ctx = SimpleNamespace(initial_state="normal", tool_parser=parser.parse_tool_call,
                        text_sm=native.TextStateMachine({"normal": [(parser.tool_call_start, "tool")],
                            "tool": [(parser.tool_call_end, "normal")]}))
                    state, calls = ResponseState(native, ctx, self.tools), []
                    for offset in range(0, len(text), size):
                        calls.extend(state.feed(text[offset:offset + size])[2])
                    self.assertEqual(len(calls), 1)
                    self.assertEqual(json.loads(calls[0]["function"]["arguments"])["content"], content)
            text = "</think>" + text
            cut = text.index(parser.tool_call_end) + len(parser.tool_call_end) - 2
            native = native_fixture(text, [cut], parser.parse_tool_call, (parser.tool_call_start, parser.tool_call_end))
            install_continuation_hooks(native)
            generator = native.ResponseGenerator()
            first = handler(native, generator)
            first.body["tools"] = self.tools
            first.handle_completion(first.handle_chat_completions(), [])
            self.assertEqual(self.calls(first), [])
            prefix = next(row["response_prefix"] for row in rows(first) if "response_prefix" in row)
            second = handler(native, generator, prefix)
            second.body["tools"] = self.tools
            second.handle_completion(second.handle_chat_completions(), [])
            calls = self.calls(second)
            self.assertEqual(len(calls), 1)
            self.assertEqual(json.loads(calls[0]["function"]["arguments"])["content"], content)

    def test_malformed_calls_never_emit_embedded_or_partial_call(self):
        for name, payload, _ in self.cases():
            parser = self.parsers[name]
            malformed = payload[:-1]  # remove required list/brace/tag closure
            text = "</think>" + parser.tool_call_start + malformed + parser.tool_call_end
            native = native_fixture(text, [], parser.parse_tool_call, (parser.tool_call_start, parser.tool_call_end))
            install_continuation_hooks(native)
            h = handler(native, native.ResponseGenerator())
            h.body["tools"] = self.tools
            with self.assertLogs(level="ERROR"):
                h.handle_completion(h.handle_chat_completions(), [])
            self.assertEqual(self.calls(h), [])
            error = next(row["error"] for row in rows(h) if "error" in row)
            self.assertIn(malformed, error["native_tool_draft"])
            self.assertIn(error["native_tool_failure"]["boundary"], ("tool_close", "stop"))
        gemma = self.parsers["gemma4"].parse_tool_call
        for payload in ('unfinished call:plan{}', 'call:plan{} trailing', 'call:write{path:<|"|>a<|"|>,path:<|"|>b<|"|>}'):
            with self.assertRaises(ValueError):
                parsed_native_calls(gemma, payload, self.tools)

    def test_gemma_serial_calls_are_admitted_only_as_a_complete_payload(self):
        parser = self.parsers["gemma4"]
        first = self.cases()[0][1]
        for tail, valid in (("call:plan{}", True), ("call:plan{", False)):
            payload = first + tail
            text = "</think>" + parser.tool_call_start + payload + parser.tool_call_end
            native = native_fixture(text, [], parser.parse_tool_call, (parser.tool_call_start, parser.tool_call_end))
            install_continuation_hooks(native)
            h = handler(native, native.ResponseGenerator())
            h.body["tools"] = self.tools
            if valid:
                h.handle_completion(h.handle_chat_completions(), [])
                self.assertEqual([c["function"]["name"] for c in self.calls(h)], ["write", "plan"])
            else:
                with self.assertLogs(level="ERROR"):
                    h.handle_completion(h.handle_chat_completions(), [])
                self.assertEqual(self.calls(h), [])
                self.assertEqual(rows(h)[-1]["error"]["native_tool_draft"], payload)


if __name__ == "__main__":
    unittest.main()
