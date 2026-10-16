"""Offline dependency smoke proof, not native model qualification."""
import hashlib
import json
import os
import platform
from pathlib import Path
import subprocess
import sys

app = Path(sys.argv[1]).resolve(strict=True)
if sys.platform != 'win32' or platform.machine().lower() not in ('amd64', 'x86_64'):
    raise RuntimeError('Windows amd64 runtime verification requires native Windows amd64')
if len(sys.argv) > 2:
    profile = sys.argv[2]
    import importlib
    root = app / 'runtime' / profile
    names = ('torch', 'transformers', 'laya', 'yaml')
    modules = {}
    for name in names:
        module = importlib.import_module(name)
        source = Path(module.__file__).resolve(strict=True)
        if not source.is_relative_to(root):
            raise RuntimeError(f'{name} came from outside the bundled profile: {source}')
        modules[name] = str(source.relative_to(app))
    # Trusted app scripts keep their sibling imports in the real launch.
    sys.path.insert(0, str(app / 'python' / 'decision'))
    import backends
    import prompts
    import server
    import mps_attention
    for module in (backends, prompts, server, mps_attention):
        if not Path(module.__file__).resolve(strict=True).is_relative_to(app / 'python' / 'decision'):
            raise RuntimeError(f'application import escaped bundle: {module.__name__}')
    print(json.dumps({'profile': profile, 'python': sys.version.split()[0], 'modules': modules, 'evidence': 'offline imports only; no checkpoint loading or judgment'}))
else:
    env = os.environ.copy()
    env.update(PYTHONPATH='Z:\\does-not-exist', PYTHONHOME='Z:\\does-not-exist', PYTHONUSERBASE='Z:\\does-not-exist', HF_HUB_OFFLINE='1', TRANSFORMERS_OFFLINE='1')
    argv = [str(app / 'runtime' / 'decision' / 'python.exe'), '-I', str(Path(__file__).resolve()), str(app), 'decision']
    report = json.loads(subprocess.check_output(argv, env=env, text=True))
    rg = subprocess.check_output([str(app / 'bin' / 'rg.exe'), '--version'], text=True).splitlines()[0]
    locks = {'decision': hashlib.sha256((app / 'runtime' / 'decision' / 'requirements.lock').read_bytes()).hexdigest()}
    sources_path = app / 'runtime' / 'sources.json'
    sources = json.loads(sources_path.read_text()) if sources_path.exists() else {}
    if report['python'] != sources['python']['version']:
        raise RuntimeError('private Python version differs from pinned source')
    if not list((app / 'templates').glob('*.tmpl')) or not (app / 'templates' / 'prompts.yaml').is_file():
        raise RuntimeError('bundled templates/prompts missing')
    print(json.dumps({'format': 1, 'platform': 'windows-amd64', 'python': report['python'], 'sources': sources, 'locks': locks, 'profiles': [report], 'ripgrep': rg}, indent=2))
