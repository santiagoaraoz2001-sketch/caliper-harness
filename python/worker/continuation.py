"""Literal unfinished-response prefills for the owned first-party MLX worker."""
import ast
import copy
from dataclasses import replace
import hashlib
import io
import json
import re
import tokenize


def unique_json_pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate native JSON argument/key")
        result[key] = value
    return result


def validate_prefix(prefix):
    required = {"tokens", "raw_chunks", "initial_state", "prompt_sha256", "tool_index"}
    if (not isinstance(prefix, dict) or not required.issubset(prefix) or set(prefix) - required - {"pending_tool_draft"}
            or not isinstance(prefix["tokens"], list) or not prefix["tokens"]
            or any(type(t) is not int or t < 0 for t in prefix["tokens"])
            or not isinstance(prefix["raw_chunks"], list)
            or len(prefix["tokens"]) != len(prefix["raw_chunks"])
            or any(not isinstance(t, str) for t in prefix["raw_chunks"])
            or prefix["initial_state"] not in ("normal", "reasoning")
            or type(prefix["tool_index"]) is not int or prefix["tool_index"] < 0
            or not isinstance(prefix["prompt_sha256"], str)
            or not isinstance(prefix.get("pending_tool_draft", ""), str)):
        raise ValueError("invalid unfinished native response prefix")
    try:
        if len(bytes.fromhex(prefix["prompt_sha256"])) != 32:
            raise ValueError()
    except ValueError:
        raise ValueError("unfinished response has no original prompt identity") from None


def parsed_native_calls(parser, text, tools):
    """Admit a whole payload before the native parser can drop an unfinished tail."""
    stripped = text.strip()
    if getattr(parser, "__module__", "").rsplit(".", 1)[-1] == "pythonic":
        # Brackets also delimit Pythonic calls, not just JSON arrays. Reuse the
        # selected native AST parser, without its permissive regex fallback.
        strict = parser.__globals__.get("_parse_pythonic_tool_call")
        literals = parser.__globals__.get("_JSONLiterals")
        if not callable(strict) or not callable(literals):
            raise RuntimeError("unsupported native Pythonic AST parser")
        try:
            tree = ast.parse(stripped, mode="eval").body
        except SyntaxError as error:
            raise ValueError("unfinished or malformed native Pythonic payload") from error
        if not isinstance(tree, ast.List) or not tree.elts:
            raise ValueError("native Pythonic payload needs a complete call list")
        for call in tree.elts:
            if not isinstance(call, ast.Call) or call.args:
                raise ValueError("native Pythonic calls need named arguments")
            function = call.func
            while isinstance(function, ast.Attribute):
                function = function.value
            if not isinstance(function, ast.Name):
                raise ValueError("native Pythonic function must have a literal name")
            names = [keyword.arg for keyword in call.keywords]
            if None in names or len(set(names)) != len(names):
                raise ValueError("duplicate or expanded native Pythonic argument")
        for node in ast.walk(tree):
            if isinstance(node, ast.Dict):
                if None in node.keys:
                    raise ValueError("expanded native Pythonic dictionary")
                keys = [ast.literal_eval(literals().visit(copy.deepcopy(key))) for key in node.keys]
                try:
                    distinct = len(set(keys))
                except TypeError as error:
                    raise ValueError("invalid native Pythonic dictionary key") from error
                if distinct != len(keys):
                    raise ValueError("duplicate native Pythonic dictionary key")
        parsed = strict(stripped)
        if parsed is None:
            raise ValueError("native Pythonic payload must contain complete function calls")
        try:
            encoded = json.dumps(parsed, allow_nan=False)
            if json.loads(encoded, object_pairs_hook=unique_json_pairs) != parsed:
                raise ValueError("native Pythonic arguments change when encoded as JSON")
        except (TypeError, ValueError) as error:
            raise ValueError("native Pythonic arguments must be lossless JSON values") from error
        return parsed if isinstance(parsed, list) else [parsed]
    if getattr(parser, "__module__", "").rsplit(".", 1)[-1] == "gemma4":
        matches = list(parser.__globals__["_tool_call_regex"].finditer(stripped))
        position = 0
        for match in matches:
            if stripped[position:match.start()].strip():
                raise ValueError("unaccounted native Gemma tool text")
            converted = parser.__globals__["_gemma4_args_to_json"](match.group(2))
            json.loads(converted, object_pairs_hook=unique_json_pairs)
            position = match.end()
        if not matches or stripped[position:].strip():
            raise ValueError("unfinished native Gemma payload; entire payload refused")
    if stripped.startswith(("{", "[")):
        def nonfinite(value):
            raise ValueError("nonfinite native JSON value: " + value)
        raw = json.loads(stripped, object_pairs_hook=unique_json_pairs, parse_constant=nonfinite)
        items = raw if isinstance(raw, list) else [raw]
        if not items or any(not isinstance(item, dict) for item in items):
            raise ValueError("native tool payload must contain complete function objects")
        result = []
        for item in items:
            # GLM's JSON fallback otherwise reduces an array to only its first item.
            parsed = parser(json.dumps(item, ensure_ascii=False), tools)
            result.extend(parsed if isinstance(parsed, list) else [parsed])
        return result
    if (getattr(parser, "__module__", "").rsplit(".", 1)[-1] == "qwen3_coder"
            or stripped.startswith("<function=")):
        # Qwen's native regex parser can skip unfinished fields and overwrite
        # repeated names. Account for every field before giving it the draft;
        # any reconciliation belongs to the existing explicit recovery path.
        function = re.fullmatch(r"<function=[^\s<>]+>(.*)</function>", stripped, re.DOTALL)
        if function is None:
            raise ValueError("unfinished native function framing; entire payload refused")
        tail, names = function.group(1).strip(), set()
        field = re.compile(r"<parameter=([^\s<>]+)>(.*?)</parameter>", re.DOTALL)
        while tail:
            match = field.match(tail)
            if match is None:
                raise ValueError("unfinished native parameter framing; entire payload refused")
            if match.group(1) in names:
                raise ValueError("duplicate native parameter key")
            names.add(match.group(1))
            tail = tail[match.end():].strip()
    if getattr(parser, "__module__", "").rsplit(".", 1)[-1] in ("glm47", "laguna") and "<arg_key>" in stripped:
        # An empty arg_key tag with no paired arg_value carries no data at
        # all; Laguna emits it as framing noise around zero-argument calls.
        # Removing exactly those tags cannot discard an argument. A paired
        # empty key (followed by an arg_value) keeps refusing below.
        stripped = re.sub(r"<arg_key>\s*</arg_key>(?!\s*<arg_value>)", "", stripped)
        text = stripped
        # Laguna may frame a zero-argument call with redundant empty key tags.
        # There is no argument data to discard only when its current schema
        # explicitly permits nothing. Other payloads retain their existing validation.
        empty = re.fullmatch(r"([^\s<]+)\s*(?:<arg_key>\s*</arg_key>\s*)+<arg_value>\s*\{\s*\}\s*</arg_value>", stripped)
        if empty:
            name = empty.group(1)
            declared = [tool["function"] for tool in (tools or [])
                        if isinstance(tool, dict) and tool.get("type") == "function"
                        and isinstance(tool.get("function"), dict) and tool["function"].get("name") == name]
            parameters = declared[0].get("parameters") if len(declared) == 1 else None
            if (isinstance(parameters, dict) and set(parameters) == {"type", "properties", "required", "additionalProperties"}
                    and parameters["type"] == "object" and parameters["properties"] == {}
                    and parameters["required"] == [] and parameters["additionalProperties"] is False):
                return [parser(name, tools)]
        _, tail = stripped.split("<arg_key>", 1)
        tail = "<arg_key>" + tail
        field = re.compile(r"<arg_key>.*?</arg_key>\s*<arg_value>.*?</arg_value>", re.DOTALL)
        names = set()
        while tail:
            match = field.match(tail)
            if match is None:
                raise ValueError("unfinished native GLM argument framing; entire payload refused")
            key = match.group().split("</arg_key>", 1)[0][len("<arg_key>"):].strip()
            if not key or key in names:
                raise ValueError("duplicate or empty native GLM argument key")
            names.add(key)
            tail = tail[match.end():].strip()
    parsed = parser(text, tools)
    return parsed if isinstance(parsed, list) else [parsed]


def pythonic_history(tokenizer, request, args):
    """Bridge content-only Pythonic templates to the structured chat history."""
    parser = getattr(tokenizer, "tool_parser", None)
    template = getattr(tokenizer, "chat_template", None)
    if isinstance(template, str):
        template = re.sub(r"{#.*?#}", "", template, flags=re.DOTALL)
    think_start, think_end = getattr(tokenizer, "think_start", None), getattr(tokenizer, "think_end", None)
    tool_start, tool_end = getattr(tokenizer, "tool_call_start", None), getattr(tokenizer, "tool_call_end", None)
    if (request.request_type != "chat"
            or getattr(parser, "__module__", "").rsplit(".", 1)[-1] != "pythonic"
            or not isinstance(template, str) or "tool_calls" in template
            or not all(isinstance(marker, str) and marker for marker in (think_start, think_end, tool_start, tool_end))
            or getattr(tokenizer, "_chat_template", None) is not None):
        return request, args
    # The native template still owns roles, separators, tool declarations and
    # generation framing. Only its missing assistant fields become content.
    rendered = copy.deepcopy(request)
    for message in rendered.messages:
        if message["role"] != "assistant":
            continue
        content = message.get("content") or ""
        if isinstance(content, list):
            if any(part.get("type") != "text" for part in content):
                raise ValueError("Only 'text' content type is supported.")
            content = "".join(part["text"] for part in content)
        thinking = []
        for key in ("reasoning", "reasoning_content", "thinking"):
            value = message.pop(key, "") or ""
            if value and value not in thinking:
                thinking.append(value)
        thought = "\n".join(thinking)
        if thought and not content.startswith(think_start + thought + think_end):
            content = think_start + thought + think_end + content
        calls = message.pop("tool_calls", [])
        if calls:
            functions, expected = [], []
            for call in calls:
                function = call["function"]
                values = function["arguments"]
                if isinstance(values, str):
                    values = json.loads(values, object_pairs_hook=unique_json_pairs)
                expected.append({"name": function["name"], "arguments": values})
                arguments = ", ".join(key + "=" + json.dumps(value, ensure_ascii=False, allow_nan=False)
                                      for key, value in values.items())
                functions.append(function["name"] + "(" + arguments + ")")
            payload = "[" + ", ".join(functions) + "]"
            if parsed_native_calls(parser, payload, request.tools) != expected:
                raise ValueError("native Pythonic history changed function arguments")
            content += tool_start + payload + tool_end
        message["content"] = content
    rendered_args = copy.copy(args)
    # Context retention belongs to the existing host policy; this template's
    # default otherwise silently strips thought from all but the last assistant.
    rendered_args.chat_template_kwargs = {**(args.chat_template_kwargs or {}), "keep_past_thinking": True}
    return rendered, rendered_args


def native_tool_literal(parser, text):
    """Whether this native format is still inside an authored argument value."""
    kind = getattr(parser, "__module__", "").rsplit(".", 1)[-1]
    if kind == "gemma4":
        return text.count('<|"|>') % 2 == 1
    if kind in ("glm47", "laguna") and "<arg_value>" in text:
        return text.rfind("<arg_value>") > text.rfind("</arg_value>")
    if kind == "qwen3_coder":
        return text.rfind("<parameter=") > text.rfind("</parameter>")
    if kind == "pythonic" or text.lstrip().startswith(("{", "[")):
        try:
            for item in tokenize.generate_tokens(io.StringIO(text).readline):
                if item.type == tokenize.ERRORTOKEN and item.string in ("'", '"'):
                    return True
        except tokenize.TokenError as error:
            return error.args[0].startswith(("EOF in multi-line string", "unterminated string literal",
                                            "unterminated triple-quoted string literal"))
        except (IndentationError, SyntaxError):
            pass  # malformed non-string syntax remains the admission owner's error
    return False


class ResponseState:
    """The existing native text machine and tool parser, including their pending bytes."""
    def __init__(self, native, ctx, tools, prefix=None):
        self.native = native
        self.text_sm = ctx.text_sm
        self.state = ctx.text_sm.make_state(ctx.initial_state)
        self.previous = ctx.initial_state
        self.tool_text = ""
        self.failed_boundary = ""
        self.formatter = native.ToolCallFormatter(ctx.tool_parser, tools, True)
        kind = getattr(ctx.tool_parser, "__module__", "").rsplit(".", 1)[-1]
        self.literal_end = (ctx.tool_parser.__globals__.get("tool_call_end")
                            if kind in ("gemma4", "pythonic", "glm47", "laguna", "qwen3_coder") else None)
        self.tokens, self.raw = [], []
        if prefix is not None:
            for chunk in prefix["raw_chunks"]:
                self.feed(chunk, replay=True)
            if self.formatter._idx != prefix["tool_index"]:
                raise ValueError("unfinished response changed native tool parser state")
            self.tokens, self.raw = list(prefix["tokens"]), list(prefix["raw_chunks"])

    def calls(self, replay=False, boundary="tool_close"):
        self.failed_boundary = "" if replay else boundary
        parsed = parsed_native_calls(self.formatter._tool_parser, self.tool_text, self.formatter._tools)
        if not parsed or any(not isinstance(c, dict) or not isinstance(c.get("name"), str)
                             or not isinstance(c.get("arguments"), dict) for c in parsed):
            raise ValueError("native tool parser did not return complete function arguments")
        self.tool_text = ""
        self.failed_boundary = ""
        if replay:
            self.formatter._idx += len(parsed)
            return []
        return [self.formatter._format(copy.deepcopy(call)) for call in parsed]

    def feed(self, text, replay=False):
        content, reasoning, calls = "", "", []
        # Resolve a potential closing marker before consuming later text in the
        # same native chunk. The native machine still owns marker matching.
        for part in text if self.literal_end else [text]:
            self.state, clean, current = self.native.TextStateMachine.step(self.state, part)
            if (self.previous == "tool" and current == "normal" and self.literal_end
                    and native_tool_literal(self.formatter._tool_parser, self.tool_text)):
                self.tool_text += clean + self.literal_end
                self.state = self.text_sm.make_state("tool")
                current, clean = "tool", ""
            if current == "reasoning":
                reasoning += clean
            elif current == "tool":
                self.tool_text += clean
            elif current == "normal":
                if self.previous == "tool":
                    calls.extend(self.calls(replay))
                content += clean
            elif current is not None:
                raise ValueError("unsupported native response parser state")
            self.previous = current
        return content, reasoning, calls


def generation_args(native, handler, stop_words):
    sampling = {name: getattr(handler, name) for name in (
        "temperature", "top_p", "top_k", "min_p", "xtc_probability", "xtc_threshold")}
    logits = {name: getattr(handler, name) for name in (
        "logit_bias", "repetition_penalty", "repetition_context_size", "presence_penalty",
        "presence_context_size", "frequency_penalty", "frequency_context_size")}
    return native.GenerationArguments(
        model=native.ModelDescription(handler.requested_model, handler.requested_draft_model, handler.adapter),
        sampling=native.SamplingArguments(**sampling), logits=native.LogitsProcessorArguments(**logits),
        stop_words=stop_words, max_tokens=handler.max_tokens,
        num_draft_tokens=handler.num_draft_tokens, logprobs=handler.logprobs,
        top_logprobs=handler.top_logprobs, seed=handler.seed,
        chat_template_kwargs=handler.chat_template_kwargs)


def install_continuation_hooks(native):
    """Keep native generation/cache/sampling; replace only its streaming response framing."""
    generator, handler = native.ResponseGenerator, native.APIHandler
    tokenize, generate = getattr(generator, "_tokenize", None), getattr(generator, "generate", None)
    chat, completion = getattr(handler, "handle_chat_completions", None), getattr(handler, "handle_completion", None)
    if not all(callable(f) for f in (tokenize, generate, chat, completion)):
        raise RuntimeError("unsupported mlx_lm.server: literal continuation hooks unavailable")

    def continued_chat(self):
        request = chat(self)
        request.response_prefix = self.body.get("response_prefix")
        if request.response_prefix is not None:
            validate_prefix(request.response_prefix)
        return request

    def continued_tokenize(self, tokenizer, request, args):
        rendered, rendered_args = pythonic_history(tokenizer, request, args)
        result = tokenize(self, tokenizer, rendered, rendered_args)
        prompt, _, _, initial = result
        args._owned_base_prompt = list(prompt)
        args._owned_prompt_sha256 = hashlib.sha256(json.dumps(prompt, separators=(",", ":")).encode()).hexdigest()
        prefix = getattr(request, "response_prefix", None)
        args._owned_response_prefix = prefix
        cache = getattr(self, "prompt_cache", None)
        if cache is not None and hasattr(cache, "_owned_prefix_fetch"):
            cache.fetch_nearest_cache = cache._owned_prefix_fetch
        if prefix is None:
            return result
        validate_prefix(prefix)
        if (request.request_type != "chat" or prefix["prompt_sha256"] != args._owned_prompt_sha256
                or prefix["initial_state"] != initial):
            raise ValueError("unfinished response requires its unchanged original chat prompt/model template")
        prompt = list(prompt) + prefix["tokens"]
        # A terminal native batch caches its last emitted token too. An exact
        # continuation hit would therefore give insert_segments no decode input.
        # Ask the existing owner for the preceding real prefix; its trim/fallback
        # policy handles KV and rotating caches, then feed the actual final token.
        fetch = cache.fetch_nearest_cache
        cache._owned_prefix_fetch = fetch
        def continued_fetch(model, tokens):
            cached, rest = fetch(model, tokens)
            if rest or tokens != prompt:
                return cached, rest
            cached, rest = fetch(model, tokens[:-1])
            return cached, rest + tokens[-1:]
        cache.fetch_nearest_cache = continued_fetch
        return prompt, [prompt], ["assistant"], initial

    def continued_generate(self, request, args, progress_callback=None):
        ctx, response = generate(self, request, args, progress_callback)
        # The same first-party detokenizer retains an unfinished Unicode byte/word.
        # It is deliberately not finalized at length; finalization would lose that state.
        detokenizer = self.model_provider.tokenizer.detokenizer
        prefix = getattr(request, "response_prefix", None)
        if prefix is not None:
            try:
                for token, expected in zip(prefix["tokens"], prefix["raw_chunks"]):
                    detokenizer.add_token(token)
                    if detokenizer.last_segment != expected:
                        raise ValueError("unfinished response raw framing differs from its original tokens")
            except BaseException:
                ctx.stop()
                response.close()
                raise
        ctx._owned_prompt_sha256 = args._owned_prompt_sha256

        def decoded():
            try:
                for item in response:
                    if item.finish_reason == "stop":
                        detokenizer.finalize()
                    else:
                        detokenizer.add_token(item.token)
                    yield replace(item, text=detokenizer.last_segment)
            finally:
                ctx.stop()
                response.close()
        return ctx, decoded()

    def continued_completion(self, request, stop_words):
        prefix = getattr(request, "response_prefix", None)
        if request.request_type != "chat" or not self.stream:
            if prefix is not None:
                raise ValueError("literal response continuation requires streaming native chat")
            return completion(self, request, stop_words)
        args = generation_args(native, self, stop_words)

        def progress(processed, total):
            native.logging.info("Prompt processing progress: %s/%s", processed, total)
            self.wfile.write(f": keepalive {processed}/{total}\n\n".encode())
            self.wfile.flush()

        try:
            ctx, response = self.response_generator.generate(request, args, progress)
            try:
                state = ResponseState(native, ctx, request.tools, prefix)
            except BaseException:
                ctx.stop()
                response.close()
                raise
        except Exception as error:
            self._set_completion_headers(400)
            self.end_headers()
            self.wfile.write(json.dumps({"error": str(error)}).encode())
            return
        self._set_stream_headers(200)
        self.end_headers()
        physical_tokens, finish = 0, None

        def send(content, reasoning, calls, reason=None, unfinished=None):
            row = self.generate_response(content, reason, tool_calls=calls, reasoning_text=reasoning)
            if unfinished is not None:
                row["response_prefix"] = unfinished
            self.wfile.write(f"data: {json.dumps(row)}\n\n".encode())
            self.wfile.flush()

        try:
            for item in response:
                physical_tokens += 1
                if item.finish_reason != "stop":
                    state.tokens.append(item.token)
                    state.raw.append(item.text)
                content, reasoning, calls = state.feed(item.text)
                if item.finish_reason == "stop":
                    # A possible closing marker is still generated tool text
                    # when EOS interrupts it. Keep it in the failed draft;
                    # discarding it can even turn incomplete framing into an
                    # apparently valid call before the recovery owner sees it.
                    if state.previous == "tool":
                        state.tool_text += state.state[3]
                    state.state, _ = native.TextStateMachine.discard(state.state)
                    if state.previous == "tool" and state.tool_text:
                        calls.extend(state.calls(boundary="stop"))
                if content or reasoning or calls:
                    send(content, reasoning, calls)
                elif item.finish_reason is None:
                    # Framing/tool arguments may buffer for a long generation.
                    # A real token is activity, without admitting a partial call.
                    self.wfile.write(b": keepalive\n\n")
                    self.wfile.flush()
                if item.finish_reason is not None:
                    finish = item.finish_reason
            if finish is None:
                raise RuntimeError("native stream ended without a terminal response")
            if finish == "length":
                unfinished = {"tokens": state.tokens, "raw_chunks": state.raw,
                              "initial_state": ctx.initial_state, "prompt_sha256": ctx._owned_prompt_sha256,
                              "tool_index": state.formatter._idx}
                if state.previous == "tool":
                    unfinished["pending_tool_draft"] = state.tool_text + state.state[3]
                send("", "", [], finish, unfinished)
            else:
                send("", "", [], "tool_calls" if state.formatter._idx else finish)
            if self.stream_options and self.stream_options.get("include_usage"):
                row = self.completion_usage_response(len(ctx.prompt), physical_tokens, ctx.prompt_cache_count)
                self.wfile.write(f"data: {json.dumps(row)}\n\n".encode())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        except (ValueError, RuntimeError) as error:
            # A parser refusal is a failed response, not an executable call or
            # an unexplained EOF. Keep its generated draft available for repair.
            native.logging.error("Native response refused: %s; tool draft: %r", error, state.tool_text)
            detail = {"message": str(error), "native_tool_draft": state.tool_text}
            # Only a parser failure at an explicit call close or normal stop
            # reaches recovery. Length, replay and transport failures retain
            # their existing continuation/error handling.
            if state.failed_boundary and item.finish_reason != "length":
                detail["native_tool_failure"] = {
                    "draft": state.tool_text, "reason": str(error),
                    "boundary": state.failed_boundary, "call_index": state.formatter._idx,
                }
            row = {"error": detail}
            self.wfile.write(f"data: {json.dumps(row)}\n\n".encode())
            self.wfile.flush()
        finally:
            ctx.stop()
            response.close()

    handler.handle_chat_completions = continued_chat
    generator._tokenize = continued_tokenize
    generator.generate = continued_generate
    handler.handle_completion = continued_completion
