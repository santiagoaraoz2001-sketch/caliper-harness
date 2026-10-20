"""Actual installed MLX Pythonic parser control; no model loading or inference.

Set SPECIFIC_HARNESS_PYTHONIC_PARSER to the installed mlx_lm/tool_parsers/pythonic.py
when running outside an environment with mlx_lm installed.
"""
import importlib.util
import copy
import json
import os
from pathlib import Path
from types import SimpleNamespace
import unittest

from continuation import parsed_native_calls, pythonic_history


class PythonicCalls(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        path = os.environ.get("SPECIFIC_HARNESS_PYTHONIC_PARSER")
        if path:
            spec = importlib.util.spec_from_file_location("mlx_lm.tool_parsers.pythonic", Path(path))
            module = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(module)
        else:
            from mlx_lm.tool_parsers import pythonic as module
        cls.parser = staticmethod(module.parse_tool_call)

    def tokenizer(self):
        return SimpleNamespace(tool_parser=self.parser, chat_template='{{ message["content"] }}',
            think_start="<think>", think_end="</think>",
            tool_call_start="<|tool_call_start|>", tool_call_end="<|tool_call_end|>")

    def test_native_single_and_serial_calls_remain_exact(self):
        for payload in (
            '[plan()]',
            '[read(path="a.go")]',
            '[read(path="a.go"), read(path="b.go")]',
            '[write(path="a.py", content="line\\nquoted \\"text\\"")]',
            '[graphql(variables={"ready":true, "unset":null, "list":[1, 2]}, query="query { plans { id } }")]',
            '[schema.read()]',
        ):
            with self.subTest(payload=payload):
                native = self.parser(payload, None)
                self.assertEqual(parsed_native_calls(self.parser, payload, None), native if isinstance(native, list) else [native])

    def test_whole_payload_refuses_ambiguous_or_dropped_values(self):
        for payload in (
            '[read(path="a.go", path="b.go")]',
            '[graphql(variables={"x":1, "x":2})]',
            '[graphql(variables={true:1, True:2})]',
            '[read(path="a.go")] dropped suffix',
            'prefix [read(path="a.go")]',
            '[read(path="a.go"), read(path=)]',
            '[read(path="a.go", missing)]',
            '[read("a.go")]',
            '[read(**{"path":"a.go"})]',
            '[write(path="a.go", content=unknown)]',
            '[write(path="a.go", content=b"x")]',
            '[graphql(variables={"x":{1,2}})]',
            '[graphql(variables={"x":1j})]',
            '[graphql(variables={"x":1e1000})]',
            '[graphql(variables={"x":(1,2)})]',
            '[graphql(variables={1:"first","1":"second"})]',
            '[factory().write(path="a.go")]',
            '[read(path="a.go"), {"name":"write"}]',
            '[]',
        ):
            with self.subTest(payload=payload):
                with self.assertRaises(ValueError):
                    parsed_native_calls(self.parser, payload, None)

    def test_failed_boundary_keeps_exact_draft_for_existing_recovery(self):
        from continuation import install_continuation_hooks
        from test_continuation import native_fixture, handler, rows
        for draft in (
            '[read(path="a.go"), read(path=)]', '[read(path="a.go", path="b.go")]',
            '[write(path="source.go", content=b"x")]', '[graphql(variables={"x":{1,2}})]',
            '[graphql(variables={"x":1j})]', '[graphql(variables={"x":1e1000})]',
            '[graphql(variables={"x":(1,2)})]', '[graphql(variables={1:"first","1":"second"})]',
        ):
            with self.subTest(draft=draft):
                native = native_fixture('</think><tool_call>' + draft + '</tool_call>', [], self.parser)
                install_continuation_hooks(native)
                generator = native.ResponseGenerator()
                h = handler(native, generator)
                with self.assertLogs(level="ERROR"):
                    h.handle_completion(h.handle_chat_completions(), [])
                detail = rows(h)[-1]["error"]
                self.assertEqual(detail["native_tool_draft"], draft)
                self.assertEqual(detail["native_tool_failure"]["boundary"], "tool_close")
                self.assertNotIn(b'"tool_calls":', h.wfile.getvalue())
                self.assertTrue(generator.contexts[-1]._should_stop)

    def test_history_preserves_calls_arguments_results_and_auxiliary_fields_once(self):
        text = 'quotes " \\' + "\n<|tool_call_end|> </think>"
        calls = [
            {"id": "one", "type": "function", "function": {"name": "write", "arguments": json.dumps({"path": "a.go", "content": text})}},
            {"id": "two", "type": "function", "function": {"name": "read", "arguments": {"path": "b.go"}}},
        ]
        messages = [
            {"role": "user", "content": "original request"},
            {"role": "assistant", "content": "ANSWER_SENTINEL", "reasoning": "THOUGHT_SENTINEL",
             "reasoning_content": "THOUGHT_SENTINEL", "tool_calls": calls},
            {"role": "tool", "content": "RESULT_ONE", "tool_call_id": "one"},
            {"role": "tool", "content": "RESULT_TWO", "tool_call_id": "two"},
        ]
        request = SimpleNamespace(request_type="chat", messages=messages, tools=[])
        args = SimpleNamespace(chat_template_kwargs={"enable_thinking": False})
        tokenizer = self.tokenizer()
        original = copy.deepcopy(request)
        rendered, rendered_args = pythonic_history(tokenizer, request, args)
        content = rendered.messages[1]["content"]
        self.assertEqual(content.count("ANSWER_SENTINEL"), 1)
        self.assertEqual(content.count("THOUGHT_SENTINEL"), 1)
        payload = content.split("<|tool_call_start|>", 1)[1][:-len("<|tool_call_end|>")]
        self.assertEqual(parsed_native_calls(self.parser, payload, []), [
            {"name": "write", "arguments": {"path": "a.go", "content": text}},
            {"name": "read", "arguments": {"path": "b.go"}},
        ])
        self.assertEqual(rendered.messages[2:], messages[2:])
        self.assertEqual(request, original)
        self.assertEqual(args.chat_template_kwargs, {"enable_thinking": False})
        self.assertEqual(rendered_args.chat_template_kwargs, {"enable_thinking": False, "keep_past_thinking": True})

    def test_history_keeps_inline_thinking_once_and_leaves_other_templates_untouched(self):
        request = SimpleNamespace(request_type="chat", messages=[{
            "role": "assistant", "content": "<think>known</think>answer", "reasoning": "known"}], tools=[])
        args = SimpleNamespace(chat_template_kwargs=None)
        tokenizer = self.tokenizer()
        rendered, _ = pythonic_history(tokenizer, request, args)
        self.assertEqual(rendered.messages[0]["content"], "<think>known</think>answer")
        for changes in (
            {"tool_parser": json.loads},
            {"chat_template": '{{ message["content"] }}{{ message["tool_calls"] }}'},
            {"_chat_template": lambda *_: "custom"},
            {"think_start": None},
        ):
            with self.subTest(changes=changes):
                alternative = copy.copy(tokenizer)
                alternative.__dict__.update(changes)
                untouched, unchanged_args = pythonic_history(alternative, request, args)
                self.assertIs(untouched, request)
                self.assertIs(unchanged_args, args)

    def test_history_uses_declared_markers_ignores_template_comments_and_refuses_duplicate_keys(self):
        request = SimpleNamespace(request_type="chat", tools=[], messages=[{
            "role": "assistant", "content": "", "reasoning": "thought",
            "tool_calls": [{"function": {"name": "read", "arguments": '{"path":"a.go"}'}}]}])
        args = SimpleNamespace(chat_template_kwargs=None)
        tokenizer = self.tokenizer()
        tokenizer.think_start, tokenizer.think_end = "<thinking>", "</thinking>"
        tokenizer.tool_call_start, tokenizer.tool_call_end = "<calls>", "</calls>"
        tokenizer.chat_template = '{# tool_calls not implemented #}{{ message["content"] }}'
        rendered, _ = pythonic_history(tokenizer, request, args)
        self.assertEqual(rendered.messages[0]["content"], '<thinking>thought</thinking><calls>[read(path="a.go")]</calls>')
        request.messages[0]["tool_calls"][0]["function"]["arguments"] = '{"path":"a.go","path":"b.go"}'
        with self.assertRaisesRegex(ValueError, "duplicate"):
            pythonic_history(tokenizer, request, args)


if __name__ == "__main__":
    unittest.main()
