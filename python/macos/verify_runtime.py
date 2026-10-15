"""Offline dependency smoke proof, not native model qualification."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

app = Path(sys.argv[1]).resolve(strict=True)
if len(sys.argv) > 2:
    profile = sys.argv[2]
    import importlib
    root = app / 'runtime' / profile
    names = ('mlx.core', 'mlx_lm.server', 'yaml', 'transformers') if profile == 'worker' else ('torch', 'transformers', 'laya', 'yaml')
    modules = {}
    for name in names:
        module = importlib.import_module(name)
        source = Path(module.__file__).resolve(strict=True)
        if not source.is_relative_to(root):
            raise RuntimeError(f'{name} came from outside the bundled profile: {source}')
        modules[name] = str(source.relative_to(app))
    if profile == 'worker':
        # Trusted app scripts keep their sibling imports in the real launch.
        sys.path.insert(0, str(app / 'python' / 'worker'))
        import server
        import mlx_lm.server as native
        server.install_native_hooks(native, '/nonexistent-smoke-model')
        from continuation import install_continuation_hooks
        install_continuation_hooks(native)
        from framing import install_framing_hooks
        install_framing_hooks(native)
    else:
        sys.path.insert(0, str(app / 'python' / 'decision'))
        import backends
        import prompts
        import server
    print(json.dumps({'profile': profile, 'python': sys.version.split()[0], 'modules': modules, 'evidence': 'offline imports and worker API contract only; no checkpoint loading or generation'}))
else:
    reports = []
    env = os.environ.copy()
    env.update(PYTHONPATH='/does-not-exist', PYTHONHOME='/does-not-exist', PYTHONUSERBASE='/does-not-exist', HF_HUB_OFFLINE='1', TRANSFORMERS_OFFLINE='1')
    for profile in ('worker', 'decision'):
        argv = [str(app / 'runtime' / profile / 'bin' / 'python3'), '-I', str(Path(__file__).resolve()), str(app), profile]
        reports.append(json.loads(subprocess.check_output(argv, env=env, text=True)))
    rg = subprocess.check_output([str(app / 'bin' / 'rg'), '--version'], text=True).splitlines()[0]
    locks = {p: hashlib.sha256((app / 'runtime' / p / 'requirements.lock').read_bytes()).hexdigest() for p in ('worker', 'decision')}
    sources_path = app / 'runtime' / 'sources.json'
    sources = json.loads(sources_path.read_text()) if sources_path.exists() else {}
    print(json.dumps({'format': 1, 'platform': 'darwin-arm64', 'python': '3.12.12', 'sources': sources, 'locks': locks, 'profiles': reports, 'ripgrep': rg}, indent=2))
