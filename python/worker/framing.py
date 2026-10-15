"""Gemma 4 response-local no-reopen policy; native parsing remains authoritative."""


class FramingGuard:
    def __init__(self, tokenizer, prompt, initial_state, mx, messages=None):
        self.mx, self.error = mx, None
        self.prompt, self.seen = list(prompt), None
        self.mode, self.quoted, self.closed = initial_state, False, False
        self.ids = {}
        for name, marker in (("channel", "<|channel>"), ("end", "<channel|>"),
                             ("tool", "<|tool_call>"), ("tool_end", "<tool_call|>"),
                             ("quote", '<|"|>')):
            ids = tokenizer.encode(marker, add_special_tokens=False)
            if len(ids) != 1 or tokenizer.decode(ids) != marker:
                raise ValueError("Gemma framing requires atomic marker: " + name)
            self.ids[name] = ids[0]
        if len(set(self.ids.values())) != len(self.ids):
            raise ValueError("Gemma framing markers overlap")
        def ends(text):
            suffix = tokenizer.encode(text, add_special_tokens=False)
            return bool(suffix) and self.prompt[-len(suffix):] == suffix
        if initial_state not in ("normal", "reasoning"):
            raise ValueError("unsupported native Gemma initial state")
        if ends("<|channel>thought\n<channel|>"):
            self.mode, self.closed = "normal", True
        elif ends("<|turn>model\n"):
            self.mode = "normal"
        elif ends("<|channel>thought\n"):
            self.mode = "reasoning"
        elif (messages and (messages[-1].get("role") == "tool" or
                            messages[-1].get("role") == "assistant" and messages[-1].get("tool_responses"))
              and (ends("<tool_response|>") or ends("<turn|>\n"))):
            self.mode = "normal"
            self.closed = self.tool_continuation_closed(messages)
        else:
            raise ValueError("unsupported Gemma response prefill/state")

    def tool_continuation_closed(self, messages):
        # Match the current template's structural provenance, never payload tokens:
        # consecutive assistant chunks share a model turn, even with visible content.
        # Only reasoning after the last user AND with tool_calls gets a channel wrapper.
        last_user = max((i for i, m in enumerate(messages) if m.get("role") == "user"), default=-1)
        previous, closed = None, False
        for index, message in enumerate(messages):
            role = message.get("role")
            if role == "tool":
                continue
            if role != "assistant" or previous != "assistant":
                closed = False
            if (role == "assistant" and index > last_user and message.get("tool_calls")
                    and (message.get("reasoning") or message.get("reasoning_content"))):
                closed = True
            previous = role
        if previous != "assistant":
            raise ValueError("unsupported Gemma tool continuation without an assistant turn")
        return closed

    def advance(self, context):
        size = len(context)
        if self.seen is None:
            # Batch includes the full prefix; sequential cache hits supply its suffix.
            # State always comes from the admitted FULL prompt, never that suffix.
            if size == 0 or size > len(self.prompt) or context.tolist() != self.prompt[-size:]:
                raise ValueError("Gemma processor context is not the admitted prompt suffix")
            self.seen = size
        else:
            if size != self.seen + 1:
                raise ValueError("Gemma processor context did not append one token")
            token = context[self.seen:].tolist()[0]
            self.seen = size
            if self.mode == "tool":
                if token == self.ids["quote"]:
                    self.quoted = not self.quoted
                elif not self.quoted and token == self.ids["tool_end"]:
                    self.mode = "normal"
            elif token == self.ids["tool"]:
                # Native supports reasoning -> tool directly. Do not add a grammar gate.
                self.mode = "tool"
            elif token == self.ids["end"] and self.mode == "reasoning":
                self.mode, self.closed = "normal", True
            elif token == self.ids["channel"]:
                self.mode = "reasoning"
        return self.closed and self.mode == "normal"

    def __call__(self, context, logits):
        # Native batch processors run outside its per-request admission try/except.
        # Latch our failure; the response wrapper stops/raises BEFORE yielding output.
        if self.error is not None:
            return logits
        try:
            if self.advance(context):
                if self.ids["channel"] >= logits.shape[-1]:
                    raise ValueError("Gemma channel marker outside logits vocabulary")
                return self.mx.where(self.mx.arange(logits.shape[-1]) == self.ids["channel"],
                                     -float("inf"), logits)
            return logits
        except Exception as error:
            self.error = RuntimeError("Gemma framing failed: " + str(error))
            return logits


def install_framing_hooks(native):
    """Bind guards to actual native request args, without altering SDK source or routes."""
    generator = native.ResponseGenerator
    tokenize = getattr(generator, "_tokenize", None)
    processors = getattr(native, "_make_logits_processors", None)
    generate = getattr(generator, "generate", None)
    if not all(callable(method) for method in (tokenize, processors, generate)):
        raise RuntimeError("unsupported native Gemma framing hook APIs")

    def guarded_tokenize(self, tokenizer, request, args):
        args._owned_gemma_guard = None
        result = tokenize(self, tokenizer, request, args)
        if getattr(self.model_provider.model, "model_type", None) in ("gemma4", "gemma4_unified"):
            if request.request_type != "chat":
                raise ValueError("Gemma framing supports native chat requests only")
            prompt, _, _, initial_state = result
            # Admission exceptions use the native per-request error queue.
            prefix = getattr(args, "_owned_response_prefix", None)
            base = getattr(args, "_owned_base_prompt", prompt) if prefix else prompt
            guard = FramingGuard(tokenizer, base, initial_state, native.mx, request.messages)
            if prefix:
                replay = list(base)
                guard.advance(native.mx.array(replay))
                for token in prefix["tokens"]:
                    replay.append(token)
                    guard.advance(native.mx.array(replay))
                guard.prompt, guard.seen = list(prompt), None
            args._owned_gemma_guard = guard
            if not prefix and guard.mode != initial_state:
                native.logging.info("Gemma initial_state provenance: %s -> %s", initial_state, guard.mode)
                result = (*result[:3], guard.mode)
        return result

    def guarded_processors(args):
        result = processors(args)
        guard = getattr(args, "_owned_gemma_guard", None)
        return [*(result or []), guard] if guard is not None else result

    def guarded_generate(self, request, generation_args, progress_callback=None):
        ctx, response = generate(self, request, generation_args, progress_callback)
        guard = getattr(generation_args, "_owned_gemma_guard", None)
        if guard is None:
            return ctx, response
        def checked():
            try:
                if guard is not None and guard.error is not None:
                    raise guard.error
                for item in response:
                    if guard is not None and guard.error is not None:
                        raise guard.error
                    yield item
                if guard is not None and guard.error is not None:
                    raise guard.error
            finally:
                # Also preserve native cancellation on a consumer closing the stream.
                ctx.stop()
        return ctx, checked()

    generator._tokenize = guarded_tokenize
    native._make_logits_processors = guarded_processors
    generator.generate = guarded_generate
