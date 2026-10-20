#!/usr/bin/env python3
"""Exercise the shipped TUI with a real PTY and an owned catalog endpoint.

Windows uses pywinpty/ConPTY; POSIX uses its native PTY. No model is downloaded
or run. The endpoint is a catalog fixture, so this qualifies setup/navigation,
not inference. Every write stays in a new evidence directory.
"""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import select
import subprocess
import sys
import threading
import time


class Terminal:
    def __init__(self, argv, cwd, env):
        self.windows = os.name == 'nt'
        self.raw = bytearray()
        self.closed = False
        if self.windows:
            from winpty import PtyProcess
            self.process = PtyProcess.spawn(argv, cwd=str(cwd), env=env, dimensions=(40, 120), backend='0')
            self.reader = self.process.fileobj
        else:
            import fcntl
            import pty
            import struct
            import termios
            master, slave = pty.openpty()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
            self.process = subprocess.Popen(argv, cwd=cwd, env=env, stdin=slave, stdout=slave, stderr=slave)
            os.close(slave)
            self.reader = master

    def alive(self):
        return self.process.isalive() if self.windows else self.process.poll() is None

    def write(self, text):
        if self.windows:
            self.process.write(text)
        else:
            os.write(self.reader, text.encode())

    def pump(self, seconds=.4):
        start = len(self.raw)
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            if not select.select([self.reader], [], [], .05)[0]:
                continue
            try:
                chunk = self.reader.recv(65536) if self.windows else os.read(self.reader, 65536)
            except (OSError, EOFError):
                break
            if not chunk:
                break
            self.raw.extend(chunk)
            if self.alive():
                if b'\x1b[6n' in chunk:
                    self.write('\x1b[1;1R')
                if b'\x1b]11;?' in chunk:
                    self.write('\x1b]11;rgb:1010/1414/1313\x1b\\')
        return bytes(self.raw[start:])

    def send(self, text, seconds=.4):
        self.write(text)
        return self.pump(seconds)

    def expect(self, action, *needles, timeout=15):
        output = self.send(action)
        # Test deadline only: preserve transcript and fail if the actual view
        # never arrives. It neither alters nor limits product behavior.
        end = time.monotonic() + timeout
        while not all(needle in plain(output) for needle in needles) and self.alive() and time.monotonic() < end:
            output += self.pump(.2)
        if not all(needle in plain(output) for needle in needles):
            raise AssertionError('Expected screen text: ' + repr(needles))
        return output

    def field(self, value):
        self.send('\x1b[H\x0b' + value, .15)

    def quit(self):
        self.send('/quit\r')
        end = time.monotonic() + 10
        while self.alive() and time.monotonic() < end:
            self.pump(.1)
        if self.alive():
            raise AssertionError('TUI did not exit after /quit')
        return self.process.exitstatus if self.windows else self.process.returncode

    def close(self):
        if self.closed:
            return
        self.closed = True
        if self.alive():
            if self.windows:
                self.process.terminate(force=True)
            else:
                self.process.terminate()
                self.process.wait(timeout=10)
        if self.windows:
            self.process.close(force=True)
        else:
            os.close(self.reader)


def plain(raw):
    text = raw.decode('utf-8', errors='replace')
    return re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07]*(?:\x07|\x1b\\)', '', text)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app', type=Path)
    parser.add_argument('evidence', type=Path)
    parser.add_argument('--component-tests', action='store_true')
    args = parser.parse_args()
    app = args.app.resolve(strict=True)
    out = args.evidence.resolve()
    out.mkdir()  # Never overwrite previous proof.
    home, work = out / 'home', out / 'project with spaces'
    home.mkdir()
    work.mkdir()
    (work / 'keep.txt').write_text('owner work\n')
    env = dict(os.environ, HOME=str(home), USERPROFILE=str(home), APPDATA=str(home / 'AppData/Roaming'),
               LOCALAPPDATA=str(home / 'AppData/Local'), XDG_CONFIG_HOME=str(home / 'config'),
               XDG_DATA_HOME=str(home / 'data'), TERM='xterm-256color', COLORTERM='truecolor',
               CALIPER_HARNESS_REDUCED_MOTION='1', PYWINPTY_BACKEND='0')
    # This child owns a real color-capable PTY. Inherited operator/CI flags can
    # otherwise silently turn a visual proof into an unstyled text-only check.
    for key in ('CI', 'NO_COLOR', 'CLICOLOR', 'CLICOLOR_FORCE'):
        env.pop(key, None)
    for key in ('APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME'):
        Path(env[key]).mkdir(parents=True, exist_ok=True)
    env['PATH'] = str(app / 'bin') + os.pathsep + env.get('PATH', '')
    if sys.platform.startswith('linux'):
        env['LD_LIBRARY_PATH'] = str(app / 'lib')
    checks = {}
    requests = []
    terminals = []
    error = None

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def do_GET(self):
            requests.append(self.path)
            ok = self.path == '/v1/models'
            raw = json.dumps({'data': [{'id': 'fixture-coder'}]} if ok else {'error': 'unknown fixture route'}).encode()
            self.send_response(200 if ok else 404)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(raw)

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f'http://127.0.0.1:{server.server_port}/v1'
    cfg = work / 'config.yaml'
    # JSON is YAML and safely represents both Windows and POSIX paths.
    cfg.write_text(json.dumps({'model': {'server': base, 'name': 'fixture-coder', 'runtime': 'openai'},
                              'decision': {'disabled': True}, 'prompts': {'path': str(app / 'prompts.yaml')},
                              'store': {'path': str(work / 'state/kuzu')},
                              'sessions': {'path': str(work / 'state/sessions')},
                              'retention': {'session_months': 0}, 'watch': {'enabled': False},
                              'tabs': {'title_every': 0}}))
    binary = app / ('caliper.exe' if os.name == 'nt' else 'caliper-harness')
    try:
        if args.component_tests:
            testenv = dict(env, GOTOOLCHAIN='local')
            testenv.setdefault('GOPATH', '/tmp/go-path')
            testenv.setdefault('GOCACHE', '/tmp/go-build-cache')
            result = subprocess.run(['go', 'test', '-count=1', '-timeout', '240s', '-run',
                                     'TestExplicitManage|TestAssistedSetup|TestRequestObserver', './internal/tui', './internal/llm'],
                                    cwd=Path(__file__).resolve().parent.parent, env=testenv,
                                    capture_output=True, text=True)
            (out / 'components.log').write_text(result.stdout + result.stderr)
            checks['native_components'] = result.returncode == 0
            if result.returncode:
                raise AssertionError('Native component tests failed; see components.log')
        term = Terminal([str(binary), 'tui', '--config', str(cfg)], work, env)
        terminals.append(term)
        term.expect('', 'Ask anything to begin.', 'Enter send', timeout=90)
        # termenv's float conversion can round #4AF6C3's red channel down by one.
        checks['brand_mint_rendered'] = bool(re.search(rb'\x1b\[[0-9;]*38;2;7[34];246;195(?:;[0-9;]*)?m', term.raw))
        if not checks['brand_mint_rendered']:
            raise AssertionError('Actual color PTY lacks the existing brand mint')
        term.send('/setup\r')
        status = term.expect('/status\r', 'Generated tokens', 'Model request')
        checks['status_dashboard'] = True
        checks['status_readonly'] = 'Add server' not in plain(status)
        term.expect('/manage\r', 'Add server', 'Add model')
        checks['manage_controls'] = True
        before_assisted = cfg.read_bytes()
        if sys.platform == 'darwin':
            connections = home / 'Library/Application Support/caliper/connections.yaml'
        elif os.name == 'nt':
            connections = Path(env['APPDATA']) / 'caliper/connections.yaml'
        else:
            connections = Path(env['XDG_CONFIG_HOME']) / 'caliper/connections.yaml'
        assisted = term.expect('g', 'LLM-assisted setup', 'Help me connect', 'c copy full prompt')
        checks['assisted_manage_shortcut'] = True
        # Remove terminal wrapping whitespace; paths with spaces remain exact
        # apart from presentation. No clipboard write touches the operator's OS.
        checks['assisted_actual_config_path'] = ''.join(str(connections).split()) in ''.join(plain(assisted).split())
        checks['assisted_actual_project_path'] = ''.join(str(cfg).split()) in ''.join(plain(assisted).split())
        assisted_end = term.expect('\x1b[F', 'untested.')
        checks['assisted_full_document_reachable'] = 'whatremainsuntested.' in ''.join(plain(assisted_end).split())
        term.expect('\x1b', 'Models & Servers', 'Add model')
        term.field('zsetup-draft')  # Start with a key that is not a Manage action.
        draft = term.send('g')
        checks['assisted_preserves_composer'] = 'zsetup-draftg' in plain(draft) and 'LLM-assisted setup' not in plain(draft)
        term.send('\x7f' * len('zsetup-draftg'))
        term.expect('/manage assisted\r', 'LLM-assisted setup', 'Help me connect')
        checks['assisted_slash_command'] = True
        term.expect('\x1b', 'Models & Servers', 'Add model')
        checks['assisted_no_configuration_write'] = cfg.read_bytes() == before_assisted and not connections.exists()
        term.expect('n', 'Preset')
        checks['server_form'] = True
        term.field('Catalog test')
        term.send('\t')
        term.send('\t')
        term.field(base)
        term.send('\x13', 1)
        checks['server_saved'] = 'Catalog test' in cfg.read_text()
        term.send('\x1b[H')
        term.expect('e', 'Preset')
        term.field('Catalog renamed')
        term.send('\x13', 1)
        checks['server_edit_saved'] = 'Catalog renamed' in cfg.read_text() and 'Catalog test' not in cfg.read_text()
        if not checks['server_edit_saved']:
            raise AssertionError('Server-only edit did not save')
        before_backspace = cfg.read_bytes()
        term.send('/statusx')
        term.send('\x7f')
        checks['composer_backspace_preserves_connections'] = cfg.read_bytes() == before_backspace
        term.expect('\r', 'Generated tokens')
        term.expect('/manage\r', 'Add model')
        term.send('+')
        term.field('manually-added-model')
        term.send('\x13', 1)
        checks['model_saved'] = 'manually-added-model' in cfg.read_text() and 'models:' in cfg.read_text()
        term.send('/models manually-added-model\r', 1)
        term.send('u', 1)
        checks['coding_selection_saved'] = 'name: manually-added-model' in cfg.read_text()
        setup = term.send('/setup\r')
        for _ in range(4):
            setup += term.send('\r')
        checks['setup_accepts_own_coding_selection'] = 'Configuration changed outside Setup' not in plain(setup)
        term.send('/models manually-added-model\r', 1)
        term.expect('t', 'Server responds')
        checks['test_connection'] = True
        term.expect('/status\r', 'Generated tokens')
        term.send('/chat\r')
        checks['chat_available'] = term.alive()
        # Manage has rendered selected rows by this point; an empty chat does
        # not have a user turn and its composer intentionally has no surface.
        checks['selected_surface_rendered'] = bool(re.search(rb'\x1b\[[0-9;]*48;2;42;42;42(?:;[0-9;]*)?m', term.raw))
        checks['clean_exit'] = term.quit() == 0
        term.close()
        # A second actual process must use persisted connections, not prior UI state.
        reopened = Terminal([str(binary), 'tui', '--config', str(cfg)], work, dict(env, NO_COLOR='1'))
        terminals.append(reopened)
        reopened.expect('', 'Enter send', timeout=90)
        reopened.expect('/manage\r', 'Catalog renamed', 'manually-added-model')
        checks['connections_survive_reopen'] = True
        reopened.expect('/status\r', 'Generated tokens')
        checks['reopened_status'] = True
        checks['reopened_clean_exit'] = reopened.quit() == 0
        # ConPTY emits an initial SGR reset even when its child sets no style.
        # Resets are harmless; every color/style-setting sequence still fails.
        checks['explicit_no_color_respected'] = all(
            sequence in (b'\x1b[m', b'\x1b[0m')
            for sequence in re.findall(rb'\x1b\[[0-9;:]*m', reopened.raw))
        checks['project_marker_preserved'] = (work / 'keep.txt').read_text() == 'owner work\n'
        checks['only_explicit_endpoint_probed'] = bool(requests) and all(p == '/v1/models' for p in requests)
    except Exception as exc:
        error = repr(exc)
    finally:
        for index, term in enumerate(terminals):
            (out / f'terminal-{index}.ansi').write_bytes(term.raw)
            term.close()
        server.shutdown()
        server.server_close()
        result = {'platform': sys.platform, 'app': str(app), 'scope': 'real native TUI with owned catalog fixture; no inference',
                  'proof_script_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                  'terminal_environment': {'TERM': env['TERM'], 'COLORTERM': env['COLORTERM'],
                                           'cleared': ['CI', 'NO_COLOR', 'CLICOLOR', 'CLICOLOR_FORCE'],
                                           'reopened': {'NO_COLOR': '1'}},
                  'checks': checks, 'requests': requests, 'error': error}
        (out / 'RESULT.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps(result, indent=2))
    return 1 if error or not checks or not all(checks.values()) else 0


if __name__ == '__main__':
    raise SystemExit(main())
