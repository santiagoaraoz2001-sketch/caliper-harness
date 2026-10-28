#!/usr/bin/env python3
"""Observe a new Caliper TUI benchmark; raw output and control files live in TEMP.

python scripts/run-observed-benchmark.py --exe PATH --workspace PATH --config PATH
    --prompt-file PATH --output-dir TEMP/PATH
To reopen an idle recorded conversation without submitting work, replace
--prompt-file with --resume SESSION_ID and pass its prior effective-config.yaml
(JSON content with absolute sessions.path). Use a fresh TEMP output directory.
For a recorded controller metadata-sharing crash, --recover-interrupted MANIFEST
additionally verifies the archived owner identities and stable unfinished session.
It preserves unknown outcome and opens history only; continuation needs user input.

Queue UTF-8 inbox/0001.json: {"id":"0001","text":"/btw Please keep..."}.
Text is sent exactly, followed by one real Enter. Use ordinary text while idle;
use /btw or /comment while running. Option dialogs support raw Tab/arrow input
in text (e.g. "\\tMy comment" accepts the selected option with a comment).
An empty text sends Enter. Multiline prompts use bracketed paste, then Enter.
For key-only navigation use {"id":"page","text":"\\u001b[6~","input_mode":"keys"}.
Keys mode adds no Enter or paste wrapper. First verify status.json advertises
control_protocol_version=2 and input_modes includes keys. Older running controllers
ignore this field and still append Enter; editing this file does not update them.
Check status.json and receipts.jsonl: sent is NOT trace-confirmed delivery.
Put quit.json in the output directory to quit once the main run is settled.
There is no benchmark runtime deadline or automatic success declaration.
"""
import argparse
from collections import Counter, deque
from datetime import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import tempfile
import time

TERMINAL_EVENTS = frozenset(('done', 'cancelled', 'error', 'failed', 'stopped'))
MAX_LINE = 1024 * 1024
MAX_INPUT = 128 * 1024
CONTROL_PROTOCOL_VERSION = 2
INPUT_MODES = ('submit', 'keys')
# Readers without FILE_SHARE_DELETE can briefly block atomic replacement on
# Windows. Bound the pause; permanent denial must remain a visible failure.
JSON_REPLACE_RETRY_DELAYS = (.025, .05, .1, .2)


def terminal_event(kind):
    return kind in TERMINAL_EVENTS


def input_wire(text, input_mode='submit'):
    if input_mode not in INPUT_MODES:
        raise ValueError('input_mode must be submit or keys')
    if input_mode == 'keys':
        if not text:
            raise ValueError('keys input must be nonempty')
        return text
    # Existing controllers omitted Enter; keep submission explicit and testable.
    if '\n' in text or '\r' in text:
        return '\x1b[200~' + text + '\x1b[201~\r'
    return text + '\r'


def transmit(terminal, observation, record, identity, text, tracked=True, input_mode='submit'):
    """One transport write; never infer UI success or model delivery from it."""
    if input_mode == 'submit' and text.strip() == '/compact' and len(observation.pending_compactions) >= 50:
        raise ValueError('50 unconfirmed compaction submissions; inspect existing receipts before submitting more')
    submitted_at = time.time()
    terminal.write(input_wire(text, input_mode))
    record({'id': identity, 'state': 'sent', 'time': time.time(), 'text': text,
            'input_mode': input_mode, 'control_protocol_version': CONTROL_PROTOCOL_VERSION})
    if tracked:
        observation.sent(identity, text, input_mode, submitted_at)


def write_json(path, value):
    temporary = path.with_suffix(path.suffix + '.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')
    for attempt in range(len(JSON_REPLACE_RETRY_DELAYS) + 1):
        try:
            temporary.replace(path)
            return
        except PermissionError as error:
            if (getattr(error, 'winerror', None) not in (5, 32, 33)
                    or attempt == len(JSON_REPLACE_RETRY_DELAYS)):
                # Preserve the last committed JSON and pending .tmp for diagnosis.
                raise
            time.sleep(JSON_REPLACE_RETRY_DELAYS[attempt])


class CurrentScreen:
    """Bounded VT screen, separate from the diagnostic output tail."""
    def __init__(self, columns=120, rows=40):
        import pyte  # Existing audit dependency; fail before launching a child.
        self.screen = pyte.Screen(columns, rows)
        self.stream = pyte.ByteStream(self.screen)

    def feed(self, data):
        self.stream.feed(data)

    def resize(self, columns, rows):
        if not (1 <= columns <= 500 and 1 <= rows <= 200):
            raise ValueError('screen size exceeds bounded observer dimensions')
        self.screen.resize(lines=rows, columns=columns)

    def snapshot(self):
        return {'kind': 'vt_current_screen', 'columns': self.screen.columns,
                'rows': self.screen.lines, 'lines': self.screen.display,
                'cursor': {'column': self.screen.cursor.x, 'row': self.screen.cursor.y,
                           'hidden': self.screen.cursor.hidden}}

    def text(self):
        return '\n'.join(self.screen.display)


class Tail:
    """Read appended lines once; oversized context snapshots never grow memory."""
    def __init__(self, path):
        self.path, self.offset = path, 0
        self.pending, self.oversize = bytearray(), False

    def events(self):
        with self.path.open('rb') as stream:
            stream.seek(self.offset)
            # Bound work per poll as well as memory. Catch up on the next poll.
            for _ in range(64):
                chunk = stream.readline(65536)
                if not chunk:
                    break
                self.offset += len(chunk)
                if len(self.pending) < MAX_LINE:
                    self.pending.extend(chunk[:MAX_LINE-len(self.pending)])
                self.oversize |= len(self.pending) == MAX_LINE
                if not chunk.endswith(b'\n'):
                    continue
                raw = bytes(self.pending)
                self.pending.clear()
                oversized, self.oversize = self.oversize, False
                if oversized:
                    match = re.search(rb'"kind"\s*:\s*"([^"\\]+)"', raw[:1024])
                    yield {'kind': match.group(1).decode() if match else 'oversize',
                           'text': '[Oversized trace event: inspect raw session file]', 'truncated': True}
                else:
                    try:
                        yield json.loads(raw)
                    except (UnicodeDecodeError, json.JSONDecodeError) as error:
                        yield {'kind': 'controller_parse_error', 'text': str(error)}


class Observation:
    def __init__(self, prompt, workspace=None):
        self.initial_prompt = prompt
        self.workspace = os.path.normcase(os.path.abspath(workspace)) if workspace else None
        self.candidates = {}
        self.submissions = {}
        self.last_raw_prompt = None
        self.adopted_input = None
        self.require_submission = False
        self.ui_actions = deque(maxlen=50)
        self.main = None
        self.main_sessions = []
        self.active = False
        self.latest = None
        self.last_ask = None
        self.last_final = None
        self.host_outcome = None
        self.tools = Counter()
        self.pending = {}
        self.native_calls = {}
        self.last_tool = None
        self.tool_sequence = 0
        self.resumed_from = None
        self.compaction = None
        self.finished_compactions = deque(maxlen=50)
        self.pending_compactions = deque()

    def input_kind(self, text, input_mode='submit'):
        if input_mode == 'keys':
            return 'ui_action'
        if self.last_ask and self.last_ask['awaiting_result']:
            return 'answer'
        if text.strip() == '/retry':
            return 'retry'
        if text.startswith(('/btw ', '/comment ')):
            return 'steering'
        if not text.strip() or text.startswith('/') or any(ord(c) < 32 and c not in '\r\n\t' for c in text):
            return 'ui_action'
        return 'prompt'

    def sent(self, identity, text, input_mode='submit', submitted_at=None):
        input_kind = self.input_kind(text, input_mode)
        if input_kind == 'ui_action':
            if input_mode == 'submit' and text.strip() == '/compact':
                self.pending_compactions.append({'id': identity, 'session': self.main, 'state': 'sent_unconfirmed'})
            self.ui_actions.append({'id': identity, 'text': text[:300], 'input_mode': input_mode,
                                    'state': 'sent_unverified', 'time': time.time()})
            return
        expected = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', text).strip()
        if expected.startswith('/btw '):
            expected = expected[5:]
        elif expected.startswith('/comment '):
            fields = expected.split(' ', 2)
            expected = fields[-1]
        ask_id = self.last_ask['call_id'] if self.last_ask and self.last_ask['awaiting_result'] else None
        if input_kind == 'prompt':
            # The composer normalizes line endings but preserves indentation
            # and surrounding whitespace; compare that actual submission.
            expected = text.replace('\r\n', '\n').replace('\r', '\n')
        if input_kind == 'retry':
            expected = self.last_raw_prompt
        self.pending[identity] = {'text': text, 'expected': expected,
                                  'time': time.time() if submitted_at is None else submitted_at,
                                  'ask_id': ask_id, 'kind': input_kind, 'owner': self.main}

    @staticmethod
    def fresh(pending, event):
        # Actual archives carry host timestamps. Missing or malformed evidence
        # cannot establish that an input preceded this event.
        if 'time' not in event:
            return False
        try:
            return datetime.fromisoformat(event['time'].replace('Z', '+00:00')).timestamp() >= pending['time']
        except (ValueError, TypeError, AttributeError):
            return False

    def event(self, session, event):
        kind, text = event.get('kind', ''), event.get('text', '')
        if kind == 'submission':
            try:
                value = json.loads(text)
                if isinstance(value, dict) and isinstance(value.get('raw_prompt'), str) and value.get('trigger') in ('submit', 'retry') and re.fullmatch('[0-9a-f]{64}', value.get('effective_prompt_sha256', '')):
                    self.require_submission = True
                    self.submissions[session] = (value, event)
                    if len(self.submissions) > 50:
                        del self.submissions[next(iter(self.submissions))]
            except (ValueError, TypeError):
                pass
            return []
        if kind == 'prompt' and session not in self.main_sessions:
            submission, submitted = self.submissions.get(session, ({}, {}))
            valid_submission = submission.get('effective_prompt_sha256') == hashlib.sha256(text.encode('utf-8')).hexdigest()
            raw = submission['raw_prompt'] if valid_submission else text
            initial = self.main is None and self.initial_prompt.strip() == text.strip()
            # Interruption can omit the old session's terminal event. Adopt only
            # exact submitted ordinary prompts with host workspace association.
            matches = [identity for identity, p in self.pending.items()
                       if p['kind'] in ('prompt', 'retry') and p['expected'] is not None
                       and p['expected'] == raw
                       and session != p['owner'] and self.fresh(p, event)
                       and (not submission or valid_submission and self.fresh(p, submitted))
                       and (not self.require_submission or valid_submission)
                       and (p['kind'] != 'retry' or valid_submission)
                       and (not submission or submission['trigger'] == ('retry' if p['kind'] == 'retry' else 'submit'))]
            # Freshness and equal content cannot disambiguate two submissions
            # of the same kind. Do not manufacture a causal input receipt.
            followup = matches[0] if len(matches) == 1 else None
            if (initial and (not self.require_submission or valid_submission)) or followup:
                self.candidates[session] = {'event': event, 'input': followup, 'raw': raw}
                if len(self.candidates) > 50:
                    del self.candidates[next(iter(self.candidates))]
        candidate = self.candidates.get(session)
        if candidate and (self.workspace is None or (kind == 'workspace' and
                os.path.normcase(os.path.abspath(text)) == self.workspace)):
                self.main = session
                if session not in self.main_sessions:
                    self.main_sessions.append(session)
                self.active = True
                self.host_outcome = None
                self.last_ask = None
                self.last_raw_prompt = candidate['raw']
                self.adopted_input = candidate['input']
                self.candidates.pop(session, None)
                if kind == 'workspace':
                    # Prompt precedes host association; publish only after it.
                    saved_workspace, self.workspace = self.workspace, None
                    try:
                        return self.event(session, candidate['event'])
                    finally:
                        self.workspace = saved_workspace
        if session != self.main:
            return []
        if kind in ('compaction_started', 'compaction_finished'):
            try:
                value = json.loads(text)
            except (ValueError, TypeError):
                return []
            operation = value.get('operation_id') if isinstance(value, dict) else None
            if not isinstance(operation, str) or not operation:
                return []
            if operation in self.finished_compactions:
                return []
            if kind == 'compaction_started':
                # Never overwrite an unfinished operation with an unrelated ID.
                if self.compaction and self.compaction['state'] == 'running':
                    return []
                self.compaction = {'operation_id': operation, 'session': session, 'state': 'running'}
                if self.pending_compactions and self.pending_compactions[0]['session'] == session:
                    self.compaction['input_id'] = self.pending_compactions.popleft()['id']
            elif (self.compaction and self.compaction['state'] == 'running'
                  and self.compaction['operation_id'] == operation
                  and self.compaction['session'] == session
                  and value.get('outcome') in ('applied', 'unchanged', 'failed', 'cancelled', 'stale_origin')):
                self.compaction.update(state='finished', outcome=value['outcome'], error=value.get('error', ''))
                self.finished_compactions.append(operation)
            return []
        self.latest = {'kind': kind, 'time': event.get('time'), 'text': text[:1200]}
        native_message, native_entry = {}, {}
        if kind in ('native_reply', 'native_result'):
            try:
                native_entry = json.loads(text)
                native_message = native_entry.get('message', {})
            except (ValueError, AttributeError):
                pass
        if kind == 'native_reply' and native_message.get('role') == 'assistant':
            calls = native_message.get('tool_calls', [])
            self.native_calls = {call['id']: call for call in calls if call.get('id')}
            if not calls and native_message.get('content'):
                # This is model prose, not host settlement or verified completion.
                self.last_final = {'kind': kind, 'text': native_message['content'][:8000]}
        if kind == 'tool':
            name = text.split(' ', 1)[0]
            self.tools[name] += 1
            self.tool_sequence += 1
            self.last_tool = name
            if name == 'ask':
                call_id = next((identity for identity, call in self.native_calls.items()
                                if call.get('function', {}).get('name') == 'ask'), None)
                self.last_ask = {'question': text[:4000], 'awaiting_result': True,
                                 'call_id': call_id or 'text-' + str(self.tool_sequence),
                                 'native': bool(call_id)}
        if kind == 'call_resolution':
            try:
                resolution = json.loads(text)
                if resolution.get('Name') == 'ask' and resolution.get('ToolCallID'):
                    self.last_ask = {'question': json.dumps(resolution.get('Arguments', {}))[:4000],
                                     'awaiting_result': True, 'call_id': resolution['ToolCallID'], 'native': True}
            except (ValueError, AttributeError):
                pass
        if terminal_event(kind):
            self.active = False
            self.host_outcome = {'kind': kind, 'time': event.get('time'), 'text': text[:8000]}
            if self.last_ask and self.last_ask['awaiting_result']:
                self.last_ask['awaiting_result'] = False
                self.last_ask['settled_without_result'] = True
        if kind in ('done', 'reply'):
            self.last_final = {'kind': kind, 'text': text[:8000]}
        confirmation = text
        if kind in ('task_comment', 'btw_committed'):
            try:
                parsed = json.loads(text)
                confirmation = parsed.get('Text', parsed.get('text', ''))
            except json.JSONDecodeError:
                pass
        ask_result = False
        if self.last_ask and self.last_ask['awaiting_result']:
            if kind == 'native_result':
                ask_result = (native_message.get('role') == 'tool' and
                              native_message.get('tool_call_id') == self.last_ask['call_id'])
                if ask_result:
                    confirmation = native_message.get('content', '')
            elif kind == 'result':
                ask_result = not self.last_ask['native'] and self.last_tool == 'ask'
        if ask_result:
            self.last_ask['awaiting_result'] = False
            self.last_ask['result'] = confirmation[:4000]
        if kind == 'native_result':
            self.native_calls.pop(native_message.get('tool_call_id'), None)
        answered = ask_result and bool(re.search(r'(?m)^\d+: ', confirmation))
        if kind == 'native_result' and native_entry.get('kind') != 'output':
            answered = False
        confirmed = []
        if kind in ('prompt', 'user', 'task_comment', 'btw_committed') or answered:
            for identity, pending in list(self.pending.items()):
                expected = pending['expected']
                if not self.fresh(pending, event):
                    continue
                if kind == 'prompt':
                    if identity != self.adopted_input:
                        continue
                elif pending['kind'] in ('prompt', 'retry'):
                    continue
                if pending['ask_id'] and not answered:
                    continue
                if answered and pending['ask_id'] != self.last_ask['call_id']:
                    continue
                if not answered and ((pending['kind'] == 'steering') != (kind in ('task_comment', 'btw_committed'))):
                    continue
                # Empty Enter needs an actual ask result, never a consumed file.
                if kind == 'prompt' or (answered and (not expected or expected in confirmation)) or (expected and expected == confirmation.strip()):
                    receipt = {'id': identity, 'state': 'trace_confirmed', 'session': session,
                               'event': kind, 'time': event.get('time')}
                    if answered:
                        receipt.update({'ask_call_id': self.last_ask['call_id'], 'result': confirmation[:4000]})
                    confirmed.append(receipt)
                    del self.pending[identity]
        return confirmed

    def status(self):
        return {'control_protocol_version': CONTROL_PROTOCOL_VERSION, 'input_modes': list(INPUT_MODES),
                'submission_provenance': 'structured_required' if self.require_submission else 'legacy_exact_prompt_only',
                'main_session': self.main, 'main_sessions': self.main_sessions,
                'resumed_from': self.resumed_from,
                'compaction': self.compaction, 'pending_compactions': list(self.pending_compactions),
                'active': self.active, 'host_settled': bool(self.host_outcome),
                'busy': self.active or bool(self.pending_compactions) or bool(self.compaction and self.compaction['state'] == 'running'),
                'settled': self.can_quit(),
                'latest_event': self.latest, 'tool_counts': dict(self.tools),
                'last_ask': self.last_ask, 'last_final': self.last_final,
                'host_outcome': self.host_outcome,
                'recent_ui_actions': list(self.ui_actions),
                'pending_delivery': [{'id': k, 'text': v['text'][:300], 'sent_at': v['time']} for k, v in self.pending.items()]}

    def can_quit(self):
        # A follow-up can be in the UI queue before its new session appears.
        pending_start = any(not p['ask_id'] and p['text'].strip() and not p['text'].startswith('/') for p in self.pending.values())
        return (bool(self.host_outcome) and not self.active and not pending_start
                and not self.pending_compactions
                and not (self.compaction and self.compaction['state'] == 'running')
                and not self.pending)


def effective_config(source, output):
    raw = source.read_text(encoding='utf-8-sig')
    try:
        config = json.loads(raw)
    except json.JSONDecodeError:
        # Avoid an extra YAML dependency or ambiguous duplicate mapping keys.
        if re.search(r'^sessions\s*:', raw, re.MULTILINE):
            raise ValueError('YAML config already defines sessions; supply JSON config or omit sessions')
        result = raw.rstrip() + '\nsessions:\n  path: ' + json.dumps(str(output / 'sessions'), ensure_ascii=False) + '\n'
    else:
        config['sessions'] = dict(config.get('sessions', {}), path=str(output / 'sessions'))
        # JSON surrogate-pair escapes are not valid YAML Unicode scalars.
        # This file is consumed as YAML; preserve non-BMP text as UTF-8.
        result = json.dumps(config, indent=2, ensure_ascii=False)
    path = output / 'effective-config.yaml'
    path.write_text(result, encoding='utf-8')
    return path


def process_alive(pid):
    """Read-only ownership guard; os.kill(pid, 0) is unsafe on Windows."""
    if not isinstance(pid, int) or pid <= 0:
        raise ValueError('prior controller has no valid process identity')
    if os.name == 'nt':
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        kernel.OpenProcess.restype = wintypes.HANDLE
        kernel.GetExitCodeProcess.argtypes = [wintypes.HANDLE, ctypes.POINTER(wintypes.DWORD)]
        kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        handle = kernel.OpenProcess(0x1000, False, pid)
        if not handle:
            return ctypes.get_last_error() != 87  # invalid PID; access denied remains possibly alive
        try:
            code = wintypes.DWORD()
            return not kernel.GetExitCodeProcess(handle, ctypes.byref(code)) or code.value == 259
        finally:
            kernel.CloseHandle(handle)
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True


def archive_identity(path):
    stat = path.stat()
    return (stat.st_dev, stat.st_ino, stat.st_size, stat.st_mtime_ns)


def interrupted_owner(config, session_id, workspace, executable, manifest_path):
    """Admit the recorded metadata-sharing crash, never a generic force resume."""
    manifest = json.loads(manifest_path.read_text(encoding='utf-8-sig'))
    source = config.parent
    if (Path(manifest['trace_directory']).resolve(strict=True) != source
            or Path(manifest['workspace']).resolve(strict=True) != workspace
            or Path(manifest['candidate']).resolve(strict=True) != executable
            or manifest.get('main_session') != session_id):
        raise ValueError('interrupted recovery manifest does not match config, workspace, executable and session')
    owner = json.loads((source / 'controller-owner.json').read_text(encoding='utf-8-sig'))
    status = json.loads((source / 'status.json').read_text(encoding='utf-8-sig'))
    controller, tui = manifest.get('controller_pid'), manifest.get('tui_pid')
    if (owner.get('pid') != controller or owner.get('tui_pid', tui) != tui
            or status.get('controller_pid', controller) != controller
            or status.get('tui_pid', tui) != tui):
        raise ValueError('interrupted recovery process identities disagree')
    if (status.get('controller_exited') is not True or status.get('active') is not True
            or status.get('host_outcome') is not None or status.get('main_session') != session_id):
        raise ValueError('interrupted recovery requires recorded controller exit and unfinished matching session')
    # Read only the bounded final traceback, never a growing model/provider log.
    error_log = source / 'controller-console.log'
    with error_log.open('rb') as stream:
        stream.seek(max(0, error_log.stat().st_size - 16384))
        error_tail = stream.read(16384).decode('utf-8', errors='replace')
    if not (all(text in error_tail for text in ('Traceback', 'write_json', 'PermissionError:', 'status.json.tmp', 'status.json'))
            and re.search(r'\[WinError (5|32|33)\]', error_tail)):
        raise ValueError('interrupted recovery requires the recorded status JSON sharing-error traceback')
    if process_alive(controller) or process_alive(tui):
        raise ValueError('interrupted recovery requires both prior owner processes gone')
    return {'reason': 'controller_metadata_sharing_failure', 'manifest': str(manifest_path),
            'controller_pid': controller, 'tui_pid': tui, 'error_log': str(error_log),
            'status_updated_at': status.get('updated_at'), 'outcome_preserved': None,
            'continuation_requires_user_input': True}


def resume_source(config, session_id, workspace, recovery_manifest=None, executable=None):
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', session_id):
        raise ValueError('resume session ID must be a single safe session name')
    try:
        value = json.loads(config.read_text(encoding='utf-8-sig'))
        path = value['sessions']['path']
    except (ValueError, KeyError, TypeError):
        raise ValueError('resume requires the prior JSON-formatted effective config with sessions.path') from None
    if not isinstance(path, str) or not Path(path).is_absolute():
        raise ValueError('resume sessions.path must be absolute; no guessed config-relative paths')
    sessions = Path(path).resolve(strict=True)
    archive = (sessions / (session_id + '.jsonl')).resolve(strict=True)
    if archive.parent != sessions or not archive.is_file():
        raise ValueError('resume archive must be a file within the configured sessions directory')
    expected = os.path.normcase(str(workspace.resolve(strict=True)))
    tail, outcome, associated, has_prompt = Tail(archive), None, False, False
    compaction_operation = None
    identity = archive_identity(archive)
    size = identity[2]
    while tail.offset < size:
        for event in tail.events():
            kind, text = event.get('kind'), event.get('text', '')
            if kind == 'controller_parse_error':
                raise ValueError('resume archive contains malformed JSON')
            if kind in ('compaction_started', 'compaction_finished'):
                try:
                    lifecycle = json.loads(text)
                    operation = lifecycle['operation_id']
                except (ValueError, TypeError, KeyError):
                    raise ValueError('resume archive contains malformed compaction lifecycle') from None
                if not isinstance(operation, str) or not operation:
                    raise ValueError('resume archive contains invalid compaction identity')
                if kind == 'compaction_started':
                    compaction_operation = operation
                elif operation == compaction_operation and lifecycle.get('outcome') in ('applied', 'unchanged', 'failed', 'cancelled', 'stale_origin'):
                    compaction_operation = None
            if kind == 'workspace':
                if not Path(text).is_absolute() or os.path.normcase(str(Path(text).resolve(strict=True))) != expected:
                    raise ValueError('resume session workspace does not match --workspace')
                associated = True
            if kind == 'prompt':
                has_prompt = True
            if kind in ('prompt', 'tool', 'native_request', 'native_reply'):
                outcome = None
            if terminal_event(kind):
                outcome = {'kind': kind, 'time': event.get('time'), 'text': text[:8000]}
    if tail.pending or tail.oversize or archive_identity(archive) != identity:
        raise ValueError('resume archive is incomplete or changing; wait for a settled idle checkpoint')
    if not associated or not has_prompt:
        raise ValueError('resume requires a recorded prompt, exact host workspace and terminal outcome')
    if compaction_operation:
        raise ValueError('manual compaction has no owned terminal receipt; prior task outcome cannot prove it settled')
    recovery = None
    if recovery_manifest:
        if outcome:
            raise ValueError('interrupted recovery is only for an archive without a terminal outcome')
        recovery = interrupted_owner(config, session_id, workspace.resolve(strict=True), executable, recovery_manifest)
        time.sleep(.1)
        if archive_identity(archive) != identity:
            raise ValueError('interrupted recovery archive is changing')
        digest = hashlib.sha256()
        with archive.open('rb') as stream:
            for chunk in iter(lambda: stream.read(65536), b''):
                digest.update(chunk)
        if archive_identity(archive) != identity:
            raise ValueError('interrupted recovery archive is changing')
        recovery['archive_sha256'] = digest.hexdigest()
    elif not outcome:
        raise ValueError('resume requires a recorded prompt, exact host workspace and terminal outcome')
    owner_path = config.parent / 'controller-owner.json'
    if owner_path.exists():
        owner = json.loads(owner_path.read_text(encoding='utf-8-sig'))
        status = json.loads((config.parent / 'status.json').read_text(encoding='utf-8-sig'))
        if not status.get('controller_exited') or process_alive(owner.get('pid')):
            raise ValueError('prior controller is not confirmed exited; no duplicate owner may launch')
        if owner.get('tui_pid') and process_alive(owner['tui_pid']):
            raise ValueError('prior TUI is still alive; no duplicate harness may launch')
    return sessions, {'session': session_id, 'workspace': expected, 'outcome': outcome,
                      'source_config': str(config), 'archive_bytes': size,
                      'archive_identity': list(identity), 'controller_interruption': recovery}


def argument_parser():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    for name in ('exe', 'workspace', 'config', 'output-dir'):
        parser.add_argument('--' + name, type=Path, required=True)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument('--prompt-file', type=Path)
    mode.add_argument('--resume', metavar='SESSION_ID')
    parser.add_argument('--recover-interrupted', type=Path, metavar='MANIFEST',
                        help='with --resume only: verify recorded controller sharing crash and dead owners; never auto-submit')
    return parser


def prepare_launch(args):
    """Validate and prepare files only; never spawn a process or submit input."""
    output = args.output_dir.resolve()
    if not output.is_relative_to(Path(tempfile.gettempdir()).resolve()):
        raise ValueError('--output-dir must be inside the system TEMP directory')
    workspace, config = args.workspace.resolve(strict=True), args.config.resolve(strict=True)
    executable = args.exe.resolve(strict=True)
    recovery_manifest = getattr(args, 'recover_interrupted', None)
    if recovery_manifest and not args.resume:
        raise ValueError('--recover-interrupted requires --resume')
    resumed, prompt = None, ''
    if args.resume:
        sessions, resumed = resume_source(config, args.resume, workspace, recovery_manifest, executable)
        if output.exists() and any(output.iterdir()):
            raise ValueError('resume requires a fresh controller output directory')
    else:
        sessions = output / 'sessions'
        if sessions.exists() and any(sessions.iterdir()):
            raise ValueError('New benchmark requires an empty sessions directory')
        prompt = args.prompt_file.read_text(encoding='utf-8-sig').strip()
        if not prompt or len(prompt.encode()) > MAX_INPUT:
            raise ValueError('Prompt must contain 1..128 KiB')
    output.mkdir(parents=True, exist_ok=True)
    with (output / 'controller-owner.json').open('x', encoding='utf-8') as owner:
        json.dump({'pid': os.getpid(), 'started': time.time(), 'resumed_from': resumed}, owner)
    for directory in (output / 'inbox', output / 'sent'):
        directory.mkdir(exist_ok=True)
    if not resumed:
        sessions.mkdir(exist_ok=True)
        config = effective_config(config, output)
    argv = [str(executable), 'tui', '--config', str(config)]
    if resumed:
        argv += ['--resume', args.resume]
    return {'output': output, 'workspace': workspace, 'config': config, 'sessions': sessions,
            'prompt': prompt, 'resumed': resumed, 'argv': argv}


def advance_opening(phase, screen, prompt, send, resume_id=None):
    if phase == 'opening' and 'Enter send' in screen:
        if resume_id:
            if 'resumed ' + resume_id in screen:
                return 'running', screen  # No tools change and no automatic prompt.
        else:
            send('controller-tools-full', '/tools full', False)
            return 'tools', ''
    elif phase == 'tools' and 'tools: full' in screen:
        send('initial-prompt', prompt)
        return 'running', screen
    return phase, screen


def launch_observation(launch):
    observation, tails = Observation(launch['prompt'], str(launch['workspace'])), {}
    if launch['resumed']:
        baseline = launch['resumed']
        observation.resumed_from = baseline
        observation.main = baseline['session']
        observation.main_sessions = [baseline['session']]
        observation.host_outcome = baseline['outcome']
        # Historical lines are baseline, never fresh delivery or activity.
        for path in launch['sessions'].glob('*.jsonl'):
            tail = Tail(path)
            tail.offset = path.stat().st_size
            if path.stem == baseline['session'] and list(archive_identity(path)) != baseline['archive_identity']:
                raise ValueError('resume archive changed after admission; wait for a stable idle checkpoint')
            tails[path.stem] = tail
    return observation, tails


def main():
    parser = argument_parser()
    args = parser.parse_args()
    frame = CurrentScreen()
    launch = prepare_launch(args)
    output, sessions, prompt = launch['output'], launch['sessions'], launch['prompt']
    inbox, sentdir = output / 'inbox', output / 'sent'
    spec = importlib.util.spec_from_file_location('caliper_terminal', Path(__file__).with_name('test-manage-status.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    env = dict(os.environ, TERM='xterm-256color', COLORTERM='truecolor', PYWINPTY_BACKEND='0')
    env['PATH'] = str(args.exe.resolve(strict=True).parent) + os.pathsep + env.get('PATH', '')
    for key in ('CI', 'NO_COLOR', 'CLICOLOR', 'CLICOLOR_FORCE'):
        env.pop(key, None)
    observation, tails = launch_observation(launch)
    terminal = module.Terminal(launch['argv'], launch['workspace'], env)
    owner_path = output / 'controller-owner.json'
    owner = json.loads(owner_path.read_text(encoding='utf-8'))
    write_json(owner_path, dict(owner, tui_pid=terminal.process.pid))
    seen = set()
    phase, output_tail, last_status, quitting = 'opening', '', 0, False
    started = time.time()
    with (output / 'terminal.raw').open('ab', buffering=0) as rawlog, (output / 'receipts.jsonl').open('a', encoding='utf-8', buffering=1) as receipts:
        def record(value):
            receipts.write(json.dumps(value, ensure_ascii=False) + '\n')
        def send(identity, text, tracked=True, input_mode='submit'):
            transmit(terminal, observation, record, identity, text, tracked, input_mode)
        try:
            while terminal.alive():
                chunk = terminal.pump(.2)
                rawlog.write(chunk)
                terminal.raw.clear()
                frame.feed(chunk)
                output_tail = (output_tail + module.plain(chunk))[-16000:]
                phase, _ = advance_opening(phase, frame.text(), prompt, send, args.resume)
                for path in sessions.glob('*.jsonl'):
                    tail = tails.setdefault(path.stem, Tail(path))
                    for event in tail.events():
                        for receipt in observation.event(path.stem, event):
                            record(receipt)
                if phase == 'running' and not quitting:
                    for path in sorted(inbox.glob('*.json')):
                        try:
                            if path.stat().st_size > MAX_INPUT:
                                raise ValueError('control file exceeds 128 KiB')
                            value = json.loads(path.read_text(encoding='utf-8-sig'))
                            identity, text = value['id'], value['text']
                            input_mode = value.get('input_mode', 'submit')
                            if not isinstance(identity, str) or not identity or not isinstance(text, str):
                                raise ValueError('id and text must be strings; id must be nonempty')
                            if identity in seen:
                                raise ValueError('duplicate control id')
                            # Validate before the pending cap so malformed raw-key
                            # controls are rejected, not silently stalled.
                            input_wire(text, input_mode)
                            if len(observation.pending) >= 50 and observation.input_kind(text, input_mode) != 'ui_action':
                                break
                            send(identity, text, input_mode=input_mode)
                            seen.add(identity)
                            path.replace(sentdir / path.name)
                        except (KeyError, ValueError) as error:
                            record({'file': path.name, 'state': 'rejected', 'reason': str(error), 'time': time.time()})
                            path.replace(sentdir / (path.name + '.rejected'))
                        # Let the UI process each action before another input.
                        break
                if phase == 'running' and (output / 'quit.json').exists() and observation.can_quit() and not quitting:
                    send('controller-quit', '/quit', False)
                    quitting = True
                now = time.time()
                if now - last_status >= 1:
                    status = observation.status()
                    status.update({'controller_pid': os.getpid(), 'tui_pid': terminal.process.pid,
                                   'phase': phase, 'alive': terminal.alive(), 'elapsed_seconds': now-started,
                                   'screen': frame.snapshot(),
                                   'screen_tail': output_tail[-8000:], 'screen_tail_kind': 'stripped_output_tail_not_screen',
                                   'ui_outcomes_verified': False, 'updated_at': now})
                    write_json(output / 'status.json', status)
                    last_status = now
        finally:
            # No success is inferred from controller exit or the model's prose.
            status = observation.status()
            status.update({'screen': frame.snapshot(), 'screen_tail': output_tail[-8000:],
                           'screen_tail_kind': 'stripped_output_tail_not_screen', 'controller_exited': True, 'tui_alive_before_close': terminal.alive(), 'updated_at': time.time()})
            try:
                write_json(output / 'status.json', status)
            finally:
                terminal.close()


if __name__ == '__main__':
    main()
