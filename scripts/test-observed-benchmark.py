#!/usr/bin/env python3
"""Controller regressions without a TUI, model, network or product mutation."""
import importlib.util
import contextlib
from datetime import datetime, timezone
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('observed', Path(__file__).with_name('run-observed-benchmark.py'))
observed = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observed)


class ControllerTests(unittest.TestCase):
    def test_effective_config_preserves_non_bmp_yaml_scalars(self):
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            output = root / 'pilot-🚀'
            output.mkdir()
            source = root / 'config.json'
            source.write_text(json.dumps({'project': {'style': 'Orbit 🚀 — café'}}), encoding='utf-8')
            path = observed.effective_config(source, output)
            raw = path.read_text(encoding='utf-8')
            self.assertNotIn('\\ud83d', raw.lower())
            self.assertEqual(json.loads(raw)['project']['style'], 'Orbit 🚀 — café')
            self.assertEqual(json.loads(raw)['sessions']['path'], str(output / 'sessions'))
            source.write_text('project:\n  style: "Orbit 🚀 — café"\n', encoding='utf-8')
            raw = observed.effective_config(source, output).read_text(encoding='utf-8')
            self.assertNotIn('\\ud83d', raw.lower())
            self.assertIn('Orbit 🚀 — café', raw)
            self.assertEqual(json.loads(raw.split('  path: ', 1)[1].strip()), str(output / 'sessions'))

    def test_composed_submission_and_retry_have_distinct_fresh_owners(self):
        state = observed.Observation('Build', '/owned')
        state.sent('first', 'Build')
        def emit(owner, kind, text, stamp=None):
            return state.event(owner, {'kind': kind, 'text': text,
                'time': stamp or datetime.fromtimestamp(time.time() + .001, timezone.utc).isoformat()})
        def submission(owner, raw, full, trigger='submit'):
            emit(owner, 'submission', json.dumps({'raw_prompt': raw,
                'trigger': trigger,
                'effective_prompt_sha256': hashlib.sha256(full.encode('utf-8')).hexdigest()}))
        emit('original', 'prompt', 'Build')
        self.assertEqual(emit('original', 'workspace', '/owned')[0]['id'], 'first')
        state.sent('next', 'Continue α')
        composed = 'User comments:\n- [src/ui.js:2] Keep amber\n\nContinue α'
        # A suffix or a claimed raw prompt without its exact hash cannot adopt.
        emit('suffix', 'prompt', composed)
        emit('suffix', 'workspace', '/owned')
        self.assertEqual(state.main, 'original')
        submission('wrong-hash', 'Continue α', 'different')
        emit('wrong-hash', 'prompt', composed)
        emit('wrong-hash', 'workspace', '/owned')
        self.assertEqual(state.main, 'original')
        submission('composed', 'Continue α', composed)
        emit('composed', 'prompt', composed)
        self.assertEqual(emit('composed', 'workspace', '/owned')[0]['id'], 'next')
        self.assertEqual(state.last_raw_prompt, 'Continue α')
        emit('composed', 'failed', 'limit')
        # Preserve an older ambiguous submission; Retry cannot confirm it.
        state.sent('ambiguous', 'Continue α')
        state.sent('retry', '/retry')
        emit('old', 'prompt', 'Continue α', '2000-01-01T00:00:00Z')
        emit('old', 'workspace', '/owned')
        emit('composed', 'prompt', composed)
        self.assertEqual(state.main, 'composed')
        # A worker with equal text cannot replace a main after host provenance
        # was established. An ordinary submit cannot impersonate Retry either.
        emit('worker', 'prompt', 'Continue α')
        emit('worker', 'workspace', '/owned')
        self.assertEqual(state.main, 'composed')
        submission('ordinary', 'Continue α', 'Continue α')
        emit('ordinary', 'prompt', 'Continue α')
        self.assertEqual(emit('ordinary', 'workspace', '/owned')[0]['id'], 'ambiguous')
        self.assertIn('retry', state.pending)
        submission('retried', 'Continue α', 'Continue α', 'retry')
        emit('retried', 'prompt', 'Continue α')
        receipts = emit('retried', 'workspace', '/owned')
        self.assertEqual([r['id'] for r in receipts], ['retry'])
        self.assertNotIn('ambiguous', state.pending)
        self.assertEqual(state.main, 'retried')

    def test_duplicate_equal_submissions_are_not_arbitrarily_attributed(self):
        state = observed.Observation('Original')
        state.event('main', {'kind': 'prompt', 'text': 'Original'})
        state.sent('one', 'Same')
        state.sent('two', 'Same')
        self.assertFalse(state.event('next', {'kind': 'prompt', 'text': 'Same',
            'time': datetime.fromtimestamp(time.time() + .001, timezone.utc).isoformat()}))
        self.assertEqual(state.main, 'main')
        self.assertEqual(set(state.pending), {'one', 'two'})

    def test_stale_or_undated_input_receipt_never_confirms(self):
        state = observed.Observation('Build')
        state.event('main', {'kind': 'prompt', 'text': 'Build'})
        state.sent('note', '/btw Keep amber')
        for stamp in (None, 'invalid', '2000-01-01T00:00:00Z'):
            event = {'kind': 'btw_committed', 'text': 'Keep amber'}
            if stamp is not None:
                event['time'] = stamp
            self.assertFalse(state.event('main', event))
        self.assertIn('note', state.pending)

    def test_transport_timestamp_precedes_fast_host_receipt(self):
        state = observed.Observation('Build')
        times = iter([100.0, 102.0])
        terminal = SimpleNamespace(write=lambda wire: None)
        with patch.object(observed.time, 'time', side_effect=lambda: next(times)):
            observed.transmit(terminal, state, lambda record: None, 'fast', 'Build')
        receipts = state.event('main', {'kind': 'prompt', 'text': 'Build',
            'time': datetime.fromtimestamp(101, timezone.utc).isoformat()})
        self.assertEqual([r['id'] for r in receipts], ['fast'])

    def test_current_screen_erases_stale_text_tracks_cursor_and_split_unicode(self):
        frame = observed.CurrentScreen(20, 4)
        frame.feed(b'old alert\r\nold second')
        frame.feed(b'\x1b[2J\x1b[Hnew ')
        unicode = 'α 🚀'.encode()
        for byte in unicode:
            frame.feed(bytes([byte]))
        self.assertNotIn('old', frame.text())
        self.assertIn('new α 🚀', frame.text())
        self.assertNotIn('�', frame.text())
        frame.feed(b'\x1b[3;4H!\x1b[?25l')
        snap = frame.snapshot()
        self.assertEqual(snap['lines'][2][3], '!')
        self.assertEqual(snap['cursor'], {'column': 4, 'row': 2, 'hidden': True})
        frame.resize(10, 2)
        snap = frame.snapshot()
        self.assertEqual((snap['columns'], snap['rows']), (10, 2))
        self.assertTrue(all(len(line) <= 10 for line in snap['lines']))
        with self.assertRaises(ValueError):
            frame.resize(501, 40)

    def test_current_screen_scroll_is_bounded_and_removes_departed_rows(self):
        frame = observed.CurrentScreen(12, 3)
        for i in range(200):
            frame.feed(('row%d\r\n' % i).encode())
        self.assertEqual(len(frame.snapshot()['lines']), 3)
        self.assertNotIn('row1 ', frame.text())
        self.assertIn('row199', frame.text())

    def test_manual_compaction_owner_outcomes_never_replace_host_settlement(self):
        for outcome in ('applied', 'unchanged', 'failed', 'cancelled', 'stale_origin'):
            with self.subTest(outcome=outcome):
                state = observed.Observation('brief')
                state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'brief'})
                state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'failed', 'text': 'original failure'})
                state.sent('compact-input', '/compact')
                self.assertFalse(state.can_quit())
                def event(owner, kind, op='owned', result=outcome):
                    state.event(owner, {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': kind, 'text': json.dumps({'operation_id': op, 'outcome': result})})
                event('foreign', 'compaction_started')
                self.assertIsNone(state.compaction)
                event('main', 'compaction_started')
                self.assertEqual(state.compaction['input_id'], 'compact-input')
                self.assertTrue(state.status()['busy'])
                self.assertFalse(state.status()['settled'])
                self.assertTrue(state.status()['host_settled'])
                event('main', 'compaction_finished', 'stale')
                state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'failed', 'text': 'original failure'})
                self.assertFalse(state.can_quit())
                event('foreign', 'compaction_finished')
                self.assertFalse(state.can_quit())
                event('main', 'compaction_finished')
                self.assertTrue(state.can_quit())
                event('main', 'compaction_started')  # Replayed completed operation is stale.
                self.assertTrue(state.can_quit())
                self.assertFalse(state.status()['busy'])
                self.assertEqual(state.host_outcome['kind'], 'failed')
                self.assertEqual(state.compaction['outcome'], outcome)
                state.sent('still-pending', '/btw retain my comment')
                self.assertFalse(state.can_quit())

    def test_compaction_missing_finish_retains_unknown_and_pending_submission(self):
        state = observed.Observation('brief')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'brief'})
        state.sent('compact', '/compact')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'compaction_started', 'text': '{"operation_id":"op"}'})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'compact', 'text': 'summary is not completion'})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'continuation', 'text': '{}'})
        self.assertIsNone(state.host_outcome)
        self.assertFalse(state.can_quit())
        state.sent('followup', 'Next work')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'compaction_finished', 'text': '{"operation_id":"op","outcome":"applied"}'})
        self.assertIn('followup', state.pending)
        self.assertFalse(state.can_quit())

    def test_json_replace_retries_transient_windows_denial_without_partial_publish(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'status.json'
            path.write_text('{"old":true}', encoding='utf-8')
            original_replace, attempts = Path.replace, []
            def replace(source, target):
                attempts.append(source.read_bytes())
                if len(attempts) < 3:
                    self.assertEqual(json.loads(target.read_text()), {'old': True})
                    error = PermissionError('reader denies delete sharing')
                    error.winerror = 5 if len(attempts) == 1 else 32
                    raise error
                return original_replace(source, target)
            with patch.object(Path, 'replace', replace), patch.object(observed.time, 'sleep') as sleep:
                observed.write_json(path, {'new': 'Unicode α — retained'})
            self.assertEqual(sleep.call_count, 2)
            self.assertEqual(len(set(attempts)), 1)
            self.assertEqual(json.loads(path.read_text(encoding='utf-8')), {'new': 'Unicode α — retained'})
            self.assertFalse(path.with_suffix('.json.tmp').exists())

    def test_json_replace_permanent_denial_is_bounded_and_preserves_committed_value(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'status.json'
            path.write_text('{"old":true}', encoding='utf-8')
            error = PermissionError('permanent denial')
            error.winerror = 5
            with patch.object(Path, 'replace', side_effect=error) as replace, patch.object(observed.time, 'sleep') as sleep:
                with self.assertRaises(PermissionError) as raised:
                    observed.write_json(path, {'new': True})
            self.assertIs(raised.exception, error)
            self.assertEqual(replace.call_count, len(observed.JSON_REPLACE_RETRY_DELAYS) + 1)
            self.assertEqual([call.args[0] for call in sleep.call_args_list], list(observed.JSON_REPLACE_RETRY_DELAYS))
            self.assertLess(sum(observed.JSON_REPLACE_RETRY_DELAYS), 1)
            self.assertEqual(json.loads(path.read_text()), {'old': True})
            self.assertEqual(json.loads(path.with_suffix('.json.tmp').read_text()), {'new': True})

    def test_json_replace_unrelated_failure_is_not_retried(self):
        for error in (OSError('disk failure'), PermissionError('non-Windows permissions')):
            with self.subTest(error=error), tempfile.TemporaryDirectory() as directory:
                with patch.object(Path, 'replace', side_effect=error) as replace, patch.object(observed.time, 'sleep') as sleep:
                    with self.assertRaises(type(error)):
                        observed.write_json(Path(directory) / 'status.json', {'new': True})
                self.assertEqual(replace.call_count, 1)
                sleep.assert_not_called()

    @unittest.skipUnless(os.name == 'nt', 'Windows delete-sharing fixture')
    def test_json_replace_recovers_after_actual_windows_reader_closes(self):
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD,
                                      wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
        kernel.CreateFileW.restype = wintypes.HANDLE
        kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        kernel.CloseHandle.restype = wintypes.BOOL
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'status.json'
            path.write_text('{"old":true}', encoding='utf-8')
            handle = kernel.CreateFileW(str(path), 0x80000000, 1, None, 3, 0, None)
            self.assertNotEqual(handle, ctypes.c_void_p(-1).value)
            released = False
            def release_reader(delay):
                nonlocal released
                self.assertEqual(json.loads(path.read_text()), {'old': True})
                self.assertTrue(kernel.CloseHandle(handle))
                released = True
            try:
                with patch.object(observed.time, 'sleep', side_effect=release_reader) as sleep:
                    observed.write_json(path, {'new': 'α'})
                self.assertEqual(sleep.call_count, 1)
                self.assertEqual(json.loads(path.read_text(encoding='utf-8')), {'new': 'α'})
            finally:
                if not released:
                    kernel.CloseHandle(handle)

    def resume_fixture(self, root):
        workspace, sessions = root / 'workspace', root / 'sessions'
        workspace.mkdir()
        sessions.mkdir()
        config = root / 'effective-config.yaml'
        config.write_text(json.dumps({'sessions': {'path': str(sessions)}, 'store': {'path': str(root / 'kuzu')},
                                     'context': {'window': 262144}}), encoding='utf-8')
        events = [{'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Original brief'}, {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'workspace', 'text': str(workspace)},
                  {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'done', 'text': 'Recorded settlement'}]
        archive = sessions / 'recorded-session.jsonl'
        archive.write_text(''.join(json.dumps(e) + '\n' for e in events), encoding='utf-8')
        exe = root / 'fixture.exe'
        exe.write_bytes(b'fixture-not-executed')
        args = SimpleNamespace(exe=exe, workspace=workspace, config=config, output_dir=root / 'resumed-output',
                               resume='recorded-session', prompt_file=None)
        return args, archive

    def test_resume_reuses_config_history_and_never_replays_historical_input(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.resume_fixture(Path(directory))
            original = args.config.read_bytes()
            launch = observed.prepare_launch(args)
            self.assertEqual(launch['config'], args.config)
            self.assertEqual(args.config.read_bytes(), original)
            self.assertEqual(launch['sessions'], archive.parent)
            self.assertEqual(launch['prompt'], '')
            self.assertEqual(launch['argv'][-2:], ['--resume', 'recorded-session'])
            self.assertFalse((args.output_dir / 'effective-config.yaml').exists())
            state, tails = observed.launch_observation(launch)
            self.assertEqual(state.main, 'recorded-session')
            self.assertFalse(state.active)
            self.assertFalse(state.pending)
            self.assertEqual(list(tails['recorded-session'].events()), [])
            self.assertEqual(tails['recorded-session'].offset, archive.stat().st_size)
            with archive.open('a', encoding='utf-8') as stream:
                stream.write(json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'changed after admission'}) + '\n')
            with self.assertRaisesRegex(ValueError, 'changed after admission'):
                observed.launch_observation(launch)
            state.sent('next', 'A new deliberate request')
            state.event('new-main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'A new deliberate request'})
            self.assertTrue(state.event('new-main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'workspace', 'text': str(args.workspace)}))
            self.assertEqual(state.main, 'new-main')

    def test_resume_rejects_unfinished_compaction_despite_prior_host_done(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.resume_fixture(Path(directory))
            def append(kind, operation):
                with archive.open('a', encoding='utf-8') as stream:
                    stream.write(json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': kind, 'text': json.dumps({'operation_id': operation, 'outcome':'failed'})}) + '\n')
            append('compaction_started', 'owned')
            append('compaction_finished', 'stale')
            with self.assertRaisesRegex(ValueError, 'no owned terminal receipt'):
                observed.prepare_launch(args)
            append('compaction_finished', 'owned')
            launch = observed.prepare_launch(args)
            self.assertEqual(launch['resumed']['outcome']['kind'], 'done')
            self.assertEqual(launch['prompt'], '')

    def interrupted_fixture(self, root):
        args, archive = self.resume_fixture(root)
        with archive.open('a', encoding='utf-8') as stream:
            stream.write(json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'interrupted work'}) + '\n')
        manifest = root / 'manifest.json'
        manifest.write_text(json.dumps({'trace_directory': str(root), 'workspace': str(args.workspace),
                                       'candidate': str(args.exe), 'main_session': args.resume,
                                       'controller_pid': 11111, 'tui_pid': 22222}), encoding='utf-8')
        (root / 'controller-owner.json').write_text(json.dumps({'pid': 11111}), encoding='utf-8')
        (root / 'status.json').write_text(json.dumps({'controller_exited': True, 'active': True,
                                                     'host_outcome': None, 'main_session': args.resume}), encoding='utf-8')
        (root / 'controller-console.log').write_text('Traceback: write_json\nPermissionError: [WinError 5] status.json.tmp -> status.json\n', encoding='utf-8')
        args.recover_interrupted = manifest
        return args, archive

    def test_interrupted_recovery_retains_unknown_outcome_and_never_submits_work(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.interrupted_fixture(Path(directory))
            original = archive.read_bytes()
            with patch.object(observed, 'process_alive', return_value=False):
                launch = observed.prepare_launch(args)
            state, tails = observed.launch_observation(launch)
            self.assertEqual(archive.read_bytes(), original)
            self.assertEqual(launch['config'], args.config)
            self.assertIsNone(state.host_outcome)
            self.assertFalse(state.status()['settled'])
            self.assertFalse(state.active)
            self.assertFalse(state.pending)
            self.assertFalse(state.can_quit())
            recovery = state.resumed_from['controller_interruption']
            self.assertEqual(recovery['archive_sha256'], observed.hashlib.sha256(original).hexdigest())
            self.assertTrue(recovery['continuation_requires_user_input'])
            self.assertEqual(list(tails[args.resume].events()), [])
            send = []
            phase, _ = observed.advance_opening('opening', 'resumed recorded-session\nEnter send', '', lambda *a: send.append(a), args.resume)
            self.assertEqual(phase, 'running')
            self.assertEqual(send, [])

    def test_interrupted_recovery_rejects_unproven_or_mismatched_owner(self):
        cases = ['manifest', 'session', 'candidate', 'owner', 'status', 'traceback', 'controller_alive', 'tui_alive']
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                args, archive = self.interrupted_fixture(root)
                if case in ('manifest', 'session', 'candidate'):
                    m = json.loads(args.recover_interrupted.read_text())
                    if case == 'manifest':
                        m['trace_directory'] = str(args.workspace)
                    elif case == 'candidate':
                        m['candidate'] = str(archive)
                    else:
                        m['main_session'] = 'another-session'
                    args.recover_interrupted.write_text(json.dumps(m))
                elif case == 'owner':
                    (root / 'controller-owner.json').write_text('{"pid":33333}')
                elif case == 'status':
                    (root / 'status.json').write_text('{"controller_exited":false}')
                elif case == 'traceback':
                    (root / 'controller-console.log').write_text('ordinary exit; no failure')
                alive = lambda pid: case == 'controller_alive' and pid == 11111 or case == 'tui_alive' and pid == 22222
                with patch.object(observed, 'process_alive', side_effect=alive), self.assertRaises(ValueError):
                    observed.prepare_launch(args)
                self.assertFalse(args.output_dir.exists())

    def test_interrupted_recovery_refuses_archive_changes_and_incomplete_json(self):
        for malformed in (False, True):
            with self.subTest(malformed=malformed), tempfile.TemporaryDirectory() as directory:
                args, archive = self.interrupted_fixture(Path(directory))
                def append(_):
                    with archive.open('a', encoding='utf-8') as stream:
                        stream.write('{"kind":"tool"}\n')
                if malformed:
                    with archive.open('a', encoding='utf-8') as stream:
                        stream.write('{"kind":')
                with patch.object(observed, 'process_alive', return_value=False), patch.object(observed.time, 'sleep', side_effect=append):
                    with self.assertRaisesRegex(ValueError, 'incomplete|changing'):
                        observed.prepare_launch(args)
                self.assertFalse(args.output_dir.exists())

    def test_interrupted_recovery_requires_resume_and_rechecks_same_size_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.interrupted_fixture(Path(directory))
            args.resume = None
            with self.assertRaisesRegex(ValueError, 'requires --resume'):
                observed.prepare_launch(args)
            args.resume = 'recorded-session'
            with patch.object(observed, 'process_alive', return_value=False):
                launch = observed.prepare_launch(args)
            stamp = archive.stat()
            os.utime(archive, ns=(stamp.st_atime_ns, stamp.st_mtime_ns + 1000000))
            with self.assertRaisesRegex(ValueError, 'changed after admission'):
                observed.launch_observation(launch)

    def test_resume_readiness_alone_never_sends_initial_prompt_or_tools(self):
        sent = []
        send = lambda *args: sent.append(args)
        phase, screen = observed.advance_opening('opening', 'Enter send', '', send, 'recorded-session')
        self.assertEqual(phase, 'opening')
        phase, screen = observed.advance_opening(phase, screen + '\nresumed recorded-session', '', send, 'recorded-session')
        self.assertEqual(phase, 'running')
        self.assertEqual(sent, [])
        # Fresh workloads keep the explicit old initialization sequence.
        phase, screen = observed.advance_opening('opening', 'Enter send', 'new brief', send)
        self.assertEqual(sent, [('controller-tools-full', '/tools full', False)])
        phase, _ = observed.advance_opening(phase, 'tools: full', 'new brief', send)
        self.assertEqual(sent[-1], ('initial-prompt', 'new brief'))

    def test_resume_rejects_missing_mismatched_and_unsettled_session(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.resume_fixture(Path(directory))
            with self.assertRaises(FileNotFoundError):
                observed.resume_source(args.config, 'missing', args.workspace)
            with self.assertRaises(ValueError):
                observed.resume_source(args.config, '../escape', args.workspace)
            other = Path(directory) / 'other'
            other.mkdir()
            with self.assertRaisesRegex(ValueError, 'workspace'):
                observed.resume_source(args.config, args.resume, other)
            with archive.open('a', encoding='utf-8') as stream:
                stream.write(json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Unfinished turn'}) + '\n')
            with self.assertRaisesRegex(ValueError, 'terminal outcome'):
                observed.prepare_launch(args)
            self.assertFalse(args.output_dir.exists())

    def test_resume_refuses_existing_owner_and_active_prior_controller(self):
        with tempfile.TemporaryDirectory() as directory:
            args, archive = self.resume_fixture(Path(directory))
            owner = args.config.parent / 'controller-owner.json'
            owner.write_text(json.dumps({'pid': os.getpid()}), encoding='utf-8')
            (args.config.parent / 'status.json').write_text(json.dumps({'controller_exited': True}), encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'duplicate owner'):
                observed.prepare_launch(args)
            owner.unlink()
            args.output_dir.mkdir()
            (args.output_dir / 'old-receipt').write_text('retained', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'fresh controller'):
                observed.prepare_launch(args)

    def test_new_run_guards_and_resume_argument_exclusivity(self):
        with tempfile.TemporaryDirectory() as directory:
            args, _ = self.resume_fixture(Path(directory))
            args.resume = None
            args.prompt_file = Path(directory) / 'brief.txt'
            args.prompt_file.write_text('new brief', encoding='utf-8')
            sessions = args.output_dir / 'sessions'
            sessions.mkdir(parents=True)
            (sessions / 'old.jsonl').write_text('{}\n', encoding='utf-8')
            with self.assertRaisesRegex(ValueError, 'empty sessions'):
                observed.prepare_launch(args)
            (sessions / 'old.jsonl').unlink()
            observed.prepare_launch(args)
            with self.assertRaises(FileExistsError):
                observed.prepare_launch(args)
            argv = ['--exe', str(args.exe), '--workspace', str(args.workspace), '--config', str(args.config),
                    '--output-dir', str(args.output_dir), '--resume', 'recorded-session', '--prompt-file', str(args.prompt_file)]
            with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
                observed.argument_parser().parse_args(argv)

    def test_keys_transport_has_no_implicit_enter_and_no_ask_delivery_claim(self):
        class Terminal:
            def __init__(self):
                self.writes = []
            def write(self, value):
                self.writes.append(value)
        terminal, receipts = Terminal(), []
        state = observed.Observation('Build')
        state.last_ask = {'awaiting_result': True, 'call_id': 'question-1'}
        for identity, keys in [('page', '\x1b[6~'), ('escape', '\x1b'), ('tab', '\t'), ('typed', 'a')]:
            observed.transmit(terminal, state, receipts.append, identity, keys, input_mode='keys')
        self.assertEqual(terminal.writes, ['\x1b[6~', '\x1b', '\t', 'a'])
        self.assertFalse(state.pending)
        self.assertTrue(state.last_ask['awaiting_result'])
        self.assertTrue(all(r['input_mode'] == 'keys' and r['state'] == 'sent' for r in receipts))
        self.assertTrue(all(r['state'] == 'sent_unverified' for r in state.ui_actions))
        # Deliberate submission still sends one Enter and awaits actual ask result.
        observed.transmit(terminal, state, receipts.append, 'answer', '')
        self.assertEqual(terminal.writes[-1], '\r')
        self.assertEqual(state.pending['answer']['ask_id'], 'question-1')

    def test_keys_mode_validates_and_advertises_capability(self):
        state = observed.Observation('Build')
        self.assertEqual(state.status()['control_protocol_version'], 2)
        self.assertIn('keys', state.status()['input_modes'])
        for mode in ('raw', '', False, None, {}, []):
            with self.assertRaises(ValueError):
                observed.input_wire('\x1b[6~', mode)
        with self.assertRaises(ValueError):
            observed.input_wire('', 'keys')
        self.assertEqual(observed.input_wire('\x1b[200~a\nb\x1b[201~', 'keys'), '\x1b[200~a\nb\x1b[201~')

    def test_interrupted_main_adopts_only_exact_prompt_and_workspace(self):
        state = observed.Observation('Build', '/owned')
        state.sent('initial', 'Build')
        self.assertFalse(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build'}))
        self.assertIsNone(state.main)
        self.assertTrue(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'workspace', 'text': '/owned'}))
        state.sent('next', 'Continue')
        state.event('foreign', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Continue'})
        state.event('foreign', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'workspace', 'text': '/other'})
        self.assertEqual(state.main, 'main')
        # No done/cancelled event from interrupted main is required.
        state.event('next-main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Continue'})
        result = state.event('next-main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'workspace', 'text': '/owned'})
        self.assertEqual(state.main, 'next-main')
        self.assertEqual(result[0]['id'], 'next')

    def test_ui_actions_do_not_block_delivery_and_are_not_claimed_successful(self):
        state = observed.Observation('Build')
        for i in range(120):
            state.sent(str(i), '/help')
        self.assertFalse(state.pending)
        self.assertEqual(len(state.status()['recent_ui_actions']), 50)
        self.assertTrue(all(x['state'] == 'sent_unverified' for x in state.ui_actions))
        state.sent('real', 'Build')
        self.assertIn('real', state.pending)

    def test_btw_receipt_requires_exact_persisted_comment(self):
        state = observed.Observation('Build')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build'})
        state.sent('note', '/btw Check docking')
        self.assertFalse(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'btw', 'text': 'Check docking'}))
        self.assertFalse(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'btw_received', 'text': 'Check docking'}))
        self.assertFalse(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'task_comment', 'text': json.dumps({'Text': 'Do not Check docking'})}))
        self.assertEqual(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'task_comment', 'text': json.dumps({'Text': 'Check docking'})})[0]['id'], 'note')
        state.sent('committed', '/btw Clear labels')
        self.assertEqual(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'btw_committed', 'text': 'Clear labels'})[0]['id'], 'committed')

    def test_real_enter_and_multiline_paste(self):
        self.assertEqual(observed.input_wire('answer'), 'answer\r')
        self.assertEqual(observed.input_wire(''), '\r')
        self.assertEqual(observed.input_wire('\tcomment'), '\tcomment\r')
        self.assertEqual(observed.input_wire('one\ntwo'), '\x1b[200~one\ntwo\x1b[201~\r')

    def test_all_terminal_events_and_worker_isolation(self):
        for kind in ('done', 'failed', 'error', 'cancelled', 'stopped'):
            state = observed.Observation('Build game')
            state.sent('first', 'Build game')
            self.assertEqual(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})[0]['state'], 'trace_confirmed')
            state.event('worker', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': kind, 'text': 'worker settled'})
            self.assertTrue(state.active)
            state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': kind, 'text': 'host reason'})
            self.assertFalse(state.active)
            self.assertEqual(state.host_outcome['kind'], kind)

    def test_sent_is_not_delivery_and_task_comment_confirms(self):
        state = observed.Observation('Build game')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})
        state.sent('comment', '/comment task-123 Check docking')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'reply', 'text': 'Check docking'})
        self.assertIn('comment', state.pending)
        result = state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'task_comment', 'text': json.dumps({'Text': 'Check docking'})})
        self.assertEqual(result[0]['id'], 'comment')
        self.assertFalse(state.pending)

    def test_followup_tracks_new_main_and_ask_result(self):
        state = observed.Observation('Build game')
        state.event('main1', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})
        state.event('main1', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'done', 'text': 'Done'})
        self.assertTrue(state.can_quit())
        state.sent('followup', 'Make landing clearer')
        self.assertFalse(state.can_quit())
        state.event('main2', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Make landing clearer'})
        self.assertEqual(state.main, 'main2')
        self.assertTrue(state.active)
        state.event('main2', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'tool', 'text': 'ask questions=Choose color'})
        state.sent('answer', '')
        self.assertIn('answer', state.pending)
        result = state.event('main2', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'result', 'text': 'tool result:\n1: Blue'})
        self.assertEqual(result[0]['id'], 'answer')

    def test_native_ask_correlates_actual_call_and_selected_result(self):
        state = observed.Observation('Build game')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})
        reply = {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'reply', 'message': {'role': 'assistant', 'content': '', 'tool_calls': [
            {'id': 'ask-42', 'type': 'function', 'function': {'name': 'ask', 'arguments': '{"question":"Palette?","options":"Blue|Orange"}'}}]}}
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'native_reply', 'text': json.dumps(reply)})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'tool', 'text': 'ask {"question":"Palette?","options":"Blue|Orange"}'})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'call_resolution', 'text': json.dumps({'ToolCallID': 'ask-42', 'Name': 'ask', 'Arguments': {'question': 'Palette?'}})})
        state.sent('choice', '\x1b[B\tkeep labels clear')
        def result(call_id, content, entry_kind='output'):
            return {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'native_result', 'text': json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': entry_kind, 'unread': True,
                'message': {'role': 'tool', 'name': 'ask', 'tool_call_id': call_id, 'content': content}})}
        # Neither another tool nor a worker's matching ID can confirm this answer.
        self.assertFalse(state.event('main', result('other-99', 'tool result:\n1: Orange — keep labels clear')))
        self.assertFalse(state.event('worker', result('ask-42', 'tool result:\n1: Orange — keep labels clear')))
        self.assertFalse(state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'user', 'text': 'keep labels clear'}))
        self.assertTrue(state.last_ask['awaiting_result'])
        receipts = state.event('main', result('ask-42', 'tool result:\n1: Orange — keep labels clear'))
        self.assertEqual(receipts[0]['id'], 'choice')
        self.assertEqual(receipts[0]['ask_call_id'], 'ask-42')
        self.assertIn('Orange', receipts[0]['result'])
        self.assertFalse(state.pending)

    def test_native_keyboard_choice_and_final_do_not_infer_completion(self):
        state = observed.Observation('Build game')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'call_resolution', 'text': json.dumps({'ToolCallID': 'ask-1', 'Name': 'ask', 'Arguments': {'question': 'Choose'}})})
        state.sent('choice', '\x1b[B')
        entry = {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'output', 'message': {'role': 'tool', 'tool_call_id': 'ask-1', 'content': 'tool result:\n1: Orange'}}
        receipts = state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'native_result', 'text': json.dumps(entry)})
        self.assertEqual(receipts[0]['id'], 'choice')
        final = {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'reply', 'message': {'role': 'assistant', 'content': 'The game is complete.'}}
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'native_reply', 'text': json.dumps(final)})
        self.assertEqual(state.last_final['text'], 'The game is complete.')
        self.assertTrue(state.active)
        self.assertIsNone(state.host_outcome)

    def test_worker_prompt_cannot_hijack_pending_free_text_answer(self):
        state = observed.Observation('Build game')
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build game'})
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'call_resolution', 'text': json.dumps({
            'ToolCallID': 'ask-language', 'Name': 'ask', 'Arguments': {'question': 'Language?'}})})
        state.sent('answer', 'Go')
        state.event('worker', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Use Go to inspect the save parser'})
        self.assertEqual(state.main, 'main')
        self.assertTrue(state.active)
        self.assertIn('answer', state.pending)
        # Even after failure, an unanswered ask is not a new ordinary prompt.
        state.event('main', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'failed', 'text': 'Request failed'})
        state.event('worker', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Use Go to inspect the save parser'})
        self.assertEqual(state.main, 'main')
        self.assertFalse(state.can_quit())  # Unconfirmed answer is preserved, not silently abandoned.
        self.assertIn('answer', state.pending)
        # A real idle follow-up still selects its newly recorded session.
        state.sent('followup', 'Continue building the game')
        state.event('worker', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Subtask context: Continue building the game'})
        self.assertEqual(state.main, 'main')
        state.event('main2', {'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Continue building the game'})
        self.assertEqual(state.main, 'main2')
        self.assertTrue(state.active)
        self.assertNotIn('followup', state.pending)

    def test_incremental_tail_partial_and_oversized_lines(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'main.jsonl'
            raw = json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'prompt', 'text': 'Build'}).encode()
            path.write_bytes(raw[:10])
            tail = observed.Tail(path)
            self.assertEqual(list(tail.events()), [])
            with path.open('ab') as stream:
                stream.write(raw[10:] + b'\n')
                stream.write(json.dumps({'time': datetime.fromtimestamp(time.time() + 0.001, timezone.utc).isoformat(), 'kind': 'failed', 'text': 'x' * (observed.MAX_LINE + 10)}).encode() + b'\n')
            rows = list(tail.events())
            self.assertEqual([row['kind'] for row in rows], ['prompt', 'failed'])
            self.assertTrue(rows[1]['truncated'])
            self.assertEqual(list(tail.events()), [])
            self.assertLessEqual(len(tail.pending), observed.MAX_LINE)


if __name__ == '__main__':
    unittest.main()
