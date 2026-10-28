"""Standalone real Windows ConPTY proof: no Caliper, model, or installed package."""
import importlib.util
import json
import subprocess
import codecs
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest

spec = importlib.util.spec_from_file_location('terminal_test_support', Path(__file__).with_name('test-manage-status.py'))
support = importlib.util.module_from_spec(spec)
spec.loader.exec_module(support)


@unittest.skipUnless(os.name == 'nt', 'real Windows ConPTY')
class RawTerminalTests(unittest.TestCase):
    def test_unicode_burst_and_input_roundtrip(self):
        with tempfile.TemporaryDirectory(prefix='caliper-raw-pty-') as directory:
            unit = 'caf\u00e9 \u65e5\u672c\u8a9e \U0001f680 \u250c\u2500\u2510'
            code = ('import os,sys; sys.stdout.write("".join(str(i)+" "+' + repr(unit + ('─'*100) + '\n') + ' for i in range(100))+"END_BURST\\n"); sys.stdout.flush(); '
                    'v=input(); print("INPUT_HEX="+v.encode("utf-8").hex(),flush=True)')
            env = dict(os.environ, PYTHONUTF8='1', PYTHONIOENCODING='utf-8')
            terminal = support.Terminal([sys.executable, '-u', '-c', code], Path(directory), env)
            try:
                until = time.monotonic() + 15
                while b'END_BURST' not in terminal.raw and time.monotonic() < until:
                    terminal.pump(.1)
                self.assertTrue(b'END_BURST' in terminal.raw, 'missing END_BURST; retained bytes='+str(len(terminal.raw)))
                terminal.send(unit + '\r', .2)
                while terminal.alive() and time.monotonic() < until:
                    terminal.pump(.1)
                terminal.pump(.1)
                text = bytes(terminal.raw).decode('utf-8', errors='strict')
                self.assertNotIn('\ufffd', text)
                self.assertIn('INPUT_HEX=' + unit.encode().hex(), text)
                self.assertGreater(len(terminal.raw), 32768)
                self.assertGreater(text.count('\U0001f680'), 0)
                self.assertEqual(terminal.process.exitstatus, 0)
                decoder = codecs.getincrementaldecoder('utf-8')('strict')
                split = ''.join(decoder.decode(bytes([b])) for b in terminal.raw) + decoder.decode(b'', final=True)
                self.assertEqual(split, text)
                evidence = os.environ.get('CALIPER_PTY_EVIDENCE')
                if evidence:
                    path = Path(evidence)
                    path.mkdir(parents=True, exist_ok=True)
                    (path / 'unicode.raw').write_bytes(terminal.raw)
                    (path / 'unicode-result.json').write_text(json.dumps(dict(bytes=len(terminal.raw), replacements=text.count('\ufffd'), exit_status=terminal.process.exitstatus, pid=terminal.process.pid, split_decode_equal=True)), encoding='utf-8')
            finally:
                terminal.close()

    def test_force_close_with_unread_full_output(self):
        # The driver subprocess imposes a deadline even on Windows releases
        # where ClosePseudoConsole waits synchronously for the output reader.
        adapter = Path(__file__).with_name('windows_terminal.py')
        child = "import sys; [sys.stdout.write('x'*8192+'\\n') for _ in range(10000)]"
        code = ("import sys,time,os; sys.path.insert(0," + repr(str(adapter.parent)) + "); "
                "from windows_terminal import RawConPTY; p=RawConPTY([sys.executable,'-u','-c'," + repr(child) + "],os.getcwd(),os.environ.copy()); "
                "time.sleep(.25); p.close(force=True); print('CLOSED',p.isalive(),flush=True)")
        result = subprocess.run([sys.executable, '-c', code], capture_output=True, text=True, timeout=12)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('CLOSED False', result.stdout)

    def test_large_input_while_child_fills_output_before_reading(self):
        # Raw console input avoids the Windows cooked-line limit. The child
        # writes a full pipe before it consumes the >32KiB Unicode request.
        with tempfile.TemporaryDirectory(prefix='caliper-raw-duplex-') as directory:
            payload = ('café 日本語 🚀 | ' * 2500) + '\x04'
            expected = payload.encode('utf-8')
            child = '''import sys,ctypes,hashlib
k=ctypes.windll.kernel32
h=k.GetStdHandle(-10)
k.SetConsoleMode(h,0)
sys.stdout.write('OUTPUT_START\\n'+''.join(str(i)+' '+('Z'*400)+'\\n' for i in range(250))+'OUTPUT_END\\n');sys.stdout.flush()
value=''
while True:
 c=sys.stdin.read(1)
 value+=c
 if c=='\\x04':break
open('received.bin','wb').write(value.encode('utf-8'))
print('INPUT_SHA='+hashlib.sha256(value.encode('utf-8')).hexdigest(),flush=True)
'''
            terminal = support.Terminal([sys.executable, '-u', '-c', child], Path(directory), dict(os.environ, PYTHONUTF8='1'))
            try:
                time.sleep(.15)
                started = time.monotonic()
                terminal.write(payload)
                deadline = time.monotonic()+15
                while terminal.alive() and time.monotonic()<deadline:
                    terminal.pump(.05)
                terminal.pump(.1)
                import hashlib
                self.assertFalse(terminal.alive(), 'duplex child did not finish')
                self.assertEqual(terminal.process.exitstatus,0)
                self.assertEqual((Path(directory)/'received.bin').read_bytes(),expected)
                self.assertIn(('INPUT_SHA='+hashlib.sha256(expected).hexdigest()).encode(),terminal.raw)
                self.assertIn(b'OUTPUT_START',terminal.raw)
                self.assertLess(time.monotonic()-started,15)
            finally:
                terminal.close()


if __name__ == '__main__':
    unittest.main(verbosity=2)
