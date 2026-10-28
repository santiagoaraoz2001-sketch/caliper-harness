#!/usr/bin/env python3
"""Real Windows ConPTY -> product input adapter -> composer/history regression.

Requires pywinpty. Builds only a fixture; never runs a model or benchmark.
Artifacts remain under system TEMP. Optional --exe reuses a built tui test binary.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

def main():
    if os.name != 'nt':
        raise SystemExit('Windows ConPTY qualification requires Windows')
    from winpty import PtyProcess
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--exe', type=Path)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix='caliper-input-pty-'))
    exe = args.exe or root / 'tui.test.exe'
    if not args.exe:
        subprocess.run(['go', 'test', '-c', '-o', str(exe), './internal/tui'], check=True)
    receipt = root / 'receipt.json'
    child = PtyProcess.spawn([str(exe), '-test.run', '^TestConsoleInputPTYHelper$'],
        env=dict(os.environ, CALIPER_INPUT_PROBE=str(receipt)), dimensions=(40,120), backend='0')
    raw = bytearray()
    def pump():
        import select
        if select.select([child.fileobj], [], [], .1)[0]:
            block = child.fileobj.recv(65536)
            raw.extend(block)
    try:
        deadline = time.monotonic() + 10
        while b'CALIPER_INPUT_READY' not in raw and time.monotonic() < deadline:
            pump()
        assert b'CALIPER_INPUT_READY' in raw, 'fixture did not become ready'
        child.setwinsize(45,130)
        resize_deadline = time.monotonic() + .6
        while time.monotonic() < resize_deadline:
            pump()
        text = 'First — “café” 🚀 日本語\r\n\r\n\tSecond paragraph'
        child.write('\x1b[200~' + text + '\x1b[201~\r')
        # FIFO keyboard events ensure Ctrl-C records after submission.
        child.write('\x03')
        deadline = time.monotonic() + 10
        while child.isalive() and time.monotonic() < deadline:
            pump()
        assert not child.isalive(), 'input reader did not terminate on quit'
        result = json.loads(receipt.read_text(encoding='utf-8'))
        assert result['history'] == [text.replace('\r\n', '\n')], result
        assert sum(k['Type'] == 13 for k in result['keys']) == 1, result
        assert result['keys'][0]['Paste'], result
        assert any(s['Width']==130 and s['Height']==45 for s in result['sizes']), result
        print('PASS real ConPTY Unicode/multiline -> exact single history submission; owned input closes. Evidence:', root)
    finally:
        (root / 'terminal.raw').write_bytes(raw)
        if child.isalive(): child.terminate(force=True)

if __name__ == '__main__': main()
