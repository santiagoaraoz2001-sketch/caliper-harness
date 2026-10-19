#!/usr/bin/env python3
"""Archive an already-built complete product; no source or work notes included."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import os
import subprocess
import tempfile
import tarfile

parser = argparse.ArgumentParser()
parser.add_argument('app', type=Path)
parser.add_argument('--version', required=True)
parser.add_argument('--out', type=Path, required=True)
args = parser.parse_args()
app = args.app.resolve(strict=True)
if not re.fullmatch(r'\d+\.\d+\.\d+', args.version):
    raise SystemExit('Version must be an immutable x.y.z release')
manifest = json.loads((app / 'runtime/manifest.json').read_text())
platform = manifest['platform']
if manifest['format'] != 1 or platform not in ('darwin-arm64', 'windows-amd64', 'linux-amd64', 'linux-arm64'):
    raise SystemExit('Unsupported managed product platform')
if platform == 'windows-amd64':
    if not (app / 'ensure-windows-runtime.ps1').is_file():
        raise SystemExit('Missing Windows system prerequisite helper')
    for file in app.rglob('*'):
        if file.is_file() and (re.match(r'(?i)^(?:(?:msvcp|vcruntime|concrt|vcomp|vccorlib)\d.*|ucrtbase)\.dll$', file.name)
                               or file.name.lower().startswith('vc_redist')):
            raise SystemExit(f'Microsoft runtime must be installed separately, not shipped: {file}')
allowed = {'caliper-harness', 'caliper.exe', 'lib', 'bin', 'runtime', 'python', 'templates', 'notices',
           'prompts.yaml', 'caliper-harness.yaml.example', 'install.sh', 'install-windows.ps1',
           'start.sh', 'start.ps1', 'ensure-windows-runtime.ps1', 'LICENSE', 'FIRST-USE.md', 'FIRST-USE-WINDOWS.md', 'FIRST-USE-LINUX.md', 'SERVER-MODELS.md',
           'PROMPTS.md', 'MANIFEST.sha256', 'kuzu_shared.dll'}
extra = {p.name for p in app.iterdir()} - allowed
if extra:
    raise SystemExit(f'Unexpected release-root files (review before shipping): {sorted(extra)}')
listed = set()
for line in (app / 'MANIFEST.sha256').read_text().splitlines():
    expected, name = line.split('  ', 1)
    if not re.fullmatch(r'[a-f0-9]{64}', expected):
        raise SystemExit(f'Invalid inventory hash: {name}')
    listed.add(name.removeprefix('./'))
    file = (app / name).resolve(strict=True)
    if not file.is_relative_to(app):
        raise SystemExit(f'Inventory escape: {name}')
    with file.open('rb') as stream:
        actual = hashlib.file_digest(stream, 'sha256').hexdigest()
    if actual != expected:
        raise SystemExit(f'Inventory mismatch: {name}')
for file in app.rglob('*'):
    if file.is_symlink():
        if not file.resolve(strict=True).is_relative_to(app):
            raise SystemExit(f'Release symlink escapes app: {file}')
    elif file.is_file() and file.name != 'MANIFEST.sha256' and str(file.relative_to(app)).replace('\\', '/') not in listed:
        raise SystemExit(f'Unlisted release file: {file}')
binary = app / ('caliper.exe' if platform.startswith('windows-') else 'caliper-harness')
with tempfile.TemporaryDirectory(prefix='caliper-release-proof-') as home:
    env = dict(os.environ, HOME=home, USERPROFILE=home, LOCALAPPDATA=home, XDG_DATA_HOME=home)
    env['PATH'] = str(app / 'bin') + os.pathsep + env.get('PATH', '')
    if platform.startswith('linux-'):
        env['LD_LIBRARY_PATH'] = str(app / 'lib')
    result = subprocess.run([str(binary), 'version'], cwd=home, env=env, capture_output=True, text=True, check=True)
    if result.stdout.strip() != args.version:
        raise SystemExit(f'Binary version {result.stdout.strip()!r} differs from requested release {args.version}')
args.out.mkdir(parents=True, exist_ok=True)
archive = args.out / f'caliper-{args.version}-{platform}.tar.gz'
if archive.exists():
    raise SystemExit(f'Refusing to replace release archive: {archive}')
with tarfile.open(archive, 'x:gz', dereference=False) as output:
    output.add(app, arcname='Caliper')
with archive.open('rb') as stream:
    digest = hashlib.file_digest(stream, 'sha256').hexdigest()
archive.with_name(archive.name + '.sha256').write_text(f'{digest}  {archive.name}\n')
size = archive.stat().st_size
parts_manifest = None
# https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases
# Each release asset must be under 2 GiB; 1 GiB transport parts leave headroom.
if size >= 2 * 1024**3:
    parts = []
    with archive.open('rb') as source:
        number = 0
        while True:
            first = source.read(1024 * 1024)
            if not first:
                break
            name = archive.name + f'.part-{number:03d}'
            part = args.out / name
            digest_part = hashlib.sha256()
            written = 0
            with part.open('xb') as output:
                chunk = first
                while chunk:
                    output.write(chunk); digest_part.update(chunk); written += len(chunk)
                    if written >= 1024**3:
                        break
                    chunk = source.read(min(1024 * 1024, 1024**3 - written))
            parts.append({'name': name, 'bytes': written, 'sha256': digest_part.hexdigest()})
            number += 1
    parts_manifest = archive.with_name(archive.name + '.parts.json')
    parts_manifest.write_text(json.dumps({'format': 1, 'archive': archive.name, 'sha256': digest, 'parts': parts}, indent=2) + '\n')
    # Remove only this invocation's assembled transport file; all source product
    # files and independently verified parts remain. No oversized asset ships.
    archive.unlink()
print(json.dumps({'version': args.version, 'platform': platform, 'archive': str(archive) if archive.exists() else None, 'parts_manifest': str(parts_manifest) if parts_manifest else None, 'bytes': size, 'sha256': digest}))
