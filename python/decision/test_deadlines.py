"""No GPU/models: prove expired and concurrent requests never queue a forward."""
import time
import unittest
from unittest.mock import patch
import server
from test_server import BackendDouble, body
from backends import Selection, Backend, BadRequest
from test_backends import ModelDouble, native


class AdmissionTests(unittest.TestCase):
    def manager(self):
        manager = server.Manager(BackendDouble)
        manager.activate(Selection())
        return manager

    def test_expired_never_forwards(self):
        manager = self.manager()
        before = len(manager.active.calls)
        with self.assertRaises(server.JudgmentExpired):
            manager.judge(body(deadline_unix_ms=(time.time() - 1) * 1000))
        self.assertEqual(len(manager.active.calls), before)

    def test_busy_never_queues(self):
        manager = self.manager()
        before = len(manager.active.calls)
        manager.judgment_lock.acquire()
        try:
            with self.assertRaises(server.JudgmentBusy):
                manager.judge(body())
        finally:
            manager.judgment_lock.release()
        self.assertEqual(len(manager.active.calls), before)
        manager.judge(body())
        self.assertEqual(len(manager.active.calls), before + 1)

    def test_expired_forward_discards_verdict_releases_lock(self):
        manager = self.manager()
        with patch.object(server.time, 'time', side_effect=[1, 1, 1, 3]):
            with self.assertRaises(server.JudgmentExpired):
                manager.judge(body(deadline_unix_ms=2000))
        self.assertFalse(manager.judgment_lock.locked())
        self.assertEqual(len(manager.active.calls), 2)

    def test_bad_deadlines_refuse(self):
        manager = self.manager()
        for deadline in [True, 'soon', -1, float('nan'), float('inf')]:
            with self.subTest(deadline=deadline), self.assertRaises(BadRequest):
                manager.judge(body(deadline_unix_ms=deadline))
        self.assertEqual(len(manager.active.calls), 1)

    def test_full_native_input_limit_before_forward(self):
        for count, limit, succeeds in [(15999, None, True), (16000, None, False), (20, 19, False), (20, 20, True), (32769, 40000, False)]:
            raw = native(); raw['usage'] = {'input_tokens': count, 'output_tokens': 0}
            model = ModelDouble(raw)
            counted = []
            def tokens(*args):
                counted.append(args)
                return count
            model.engine.tokens = tokens
            backend = Backend(Selection(runtime='system_one'), model,
                              {'native_device': 'cpu', 'device': 'cpu', 'context_limit': 32768}, lambda *args: args)
            kwargs = {} if limit is None else {'max_input_tokens': limit}
            with self.subTest(count=count, limit=limit):
                if succeeds:
                    backend.judge('context', ['yes', 'no'], 'instructions', **kwargs)
                    self.assertEqual(len(model.calls), 1)
                else:
                    name = 'decision input' if (15999 if limit is None else limit) < 32768 else 'model context'
                    with self.assertRaisesRegex(BadRequest, 'request has .* tokens; ' + name + ' limit'):
                        backend.judge('context', ['yes', 'no'], 'instructions', **kwargs)
                    self.assertEqual(model.calls, [])
                self.assertEqual(counted, [('context', [('instructions', {'yes': 'yes', 'no': 'no'})])])

if __name__ == '__main__':
    unittest.main()
