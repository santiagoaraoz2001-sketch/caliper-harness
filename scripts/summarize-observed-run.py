#!/usr/bin/env python3
"""Summarize actual Caliper session evidence; counters are not quality verdicts."""
import argparse
from collections import Counter
from datetime import datetime
import hashlib
import json
from pathlib import Path


def stamp(value):
    return datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()


def summarize(path):
    kinds, tools, repeated = Counter(), Counter(), Counter()
    first = last = first_write = None
    requests, request_seconds, peak_context, capacity_refusals = 0, 0.0, 0, 0
    failures, knowledge_calls, outcome = [], 0, None
    for raw in path.open(encoding='utf-8-sig'):
        try:
            event = json.loads(raw)
        except json.JSONDecodeError:
            continue  # A live writer can leave a partial last record.
        kind, text, when = event.get('kind'), event.get('text', ''), event.get('time')
        kinds[kind] += 1
        first, last = first or when, when
        if kind == 'tool':
            name = text.split(' ', 1)[0]
            tools[name] += 1
            repeated[(name, hashlib.sha256(text.encode()).hexdigest())] += 1
            if name in ('write', 'edit'):
                first_write = first_write or when
            if name == 'graphql' and 'knowledge' in text.lower():
                knowledge_calls += 1
        if kind == 'note' and 'refused before inference' in text:
            capacity_refusals += 1
        if kind in ('context', 'model_request', 'native_result'):
            try:
                data = json.loads(text)
            except json.JSONDecodeError:
                continue
            if kind == 'context':
                peak_context = max(peak_context, data.get('prompt', 0) or 0)
            if kind == 'model_request':
                requests += 1
                request_seconds += data.get('seconds', 0)
                peak_context = max(peak_context, data.get('usage', {}).get('In', 0) or 0)
            if kind == 'native_result' and data.get('kind') == 'failure':
                message = data.get('message', {})
                failures.append({'time': when, 'tool': message.get('name'),
                                 'call_id': message.get('tool_call_id'), 'excerpt': message.get('content', '')[:800]})
        if kind in ('failed', 'done', 'error', 'cancelled', 'stopped'):
            outcome = {'kind': kind, 'time': when, 'text': text[:3000]}
    return {'session': path.stem, 'first_event': first, 'last_event': last,
            'elapsed_seconds': stamp(last)-stamp(first) if first and last else None,
            'first_edit_or_write_seconds': stamp(first_write)-stamp(first) if first_write else None,
            'tool_counts': dict(tools), 'model_requests': requests, 'model_request_seconds': round(request_seconds, 3),
            'peak_reported_prompt_tokens': peak_context, 'local_capacity_refusals': capacity_refusals,
            'knowledge_tool_calls': knowledge_calls, 'native_tool_failures': failures,
            'repeated_identical_calls': [{'tool': name, 'argument_hash': digest, 'count': count}
                                         for (name, digest), count in repeated.items() if count > 1],
            'event_counts': dict(kinds), 'host_outcome': outcome,
            'limits': 'Repeated calls can be legitimate. First write excludes shell-created files. Wall time includes user waits. Inspect artifact and receipts before judging quality.'}


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('session', type=Path)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    args.output.write_text(json.dumps(summarize(args.session), indent=2), encoding='utf-8')
