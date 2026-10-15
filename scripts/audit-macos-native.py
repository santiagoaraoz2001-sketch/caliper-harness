"""Builder audit/signing of every native dependency shipped in the folder."""
from pathlib import Path
from functools import cache
import re
import subprocess
import sys

app = Path(sys.argv[1]).resolve(strict=True)
magic = {b'\xcf\xfa\xed\xfe', b'\xfe\xed\xfa\xcf', b'\xca\xfe\xba\xbe', b'\xbe\xba\xfe\xca'}

def output(*args):
    return subprocess.check_output(args, text=True)

def apple(path):
    return str(path).startswith(('/usr/lib/', '/System/Library/'))

def expand(value, loader, executable):
    for prefix, base in (('@loader_path', loader.parent), ('@executable_path', executable.parent)):
        if value == prefix or value.startswith(prefix + '/'):
            return (base / value[len(prefix):].lstrip('/')).resolve()
    if value.startswith('/'):
        return Path(value).resolve()
    raise RuntimeError(f'unresolved loader path: {loader}: {value}')

@cache
def rpaths(loader, executable):
    values = re.findall(r'cmd LC_RPATH\s+cmdsize \d+\s+path (.*?) \(offset', output('otool', '-l', str(loader)))
    paths = [expand(v, loader, executable) for v in values]
    for p in paths:
        if not p.is_relative_to(app) and not apple(p):
            raise RuntimeError(f'external native RPATH: {loader}: {p}')
    return paths

native = []
for p in sorted(app.rglob('*')):
    if not p.is_file() or p.is_symlink():
        continue
    with p.open('rb') as stream:
        if stream.read(4) in magic:
            native.append(p)

metadata = {}
for p in native:
    identities = set(output('otool', '-D', str(p)).splitlines()[1:])
    deps = [line.strip().split(' (', 1)[0] for line in output('otool', '-L', str(p)).splitlines()[1:]]
    metadata[p] = [dep for dep in deps if dep not in identities]

def inherited_paths(loader, executable, context, visited=None):
    # dyld inherits LC_RPATH from the chain of images which loaded a dylib.
    # MLX's extension, for example, supplies the search path used by libmlx.
    visited = set() if visited is None else visited
    if loader in visited:
        return []
    visited.add(loader)
    paths = list(rpaths(loader, executable))
    for parent, deps in metadata.items():
        if parent.is_relative_to(context) and any(Path(dep).name == loader.name for dep in deps):
            paths += inherited_paths(parent, executable, context, visited)
    return paths

for p in native:
    arches = output('lipo', '-archs', str(p)).split()
    if 'arm64' not in arches:
        raise RuntimeError(f'no Apple Silicon slice: {p}')
    load_commands = output('otool', '-l', str(p))
    minimums = re.findall(r'cmd LC_BUILD_VERSION\s+cmdsize \d+\s+platform 1\s+minos ([0-9.]+)',load_commands)
    minimums += re.findall(r'cmd LC_VERSION_MIN_MACOSX\s+cmdsize \d+\s+version ([0-9.]+)',load_commands)
    for version in minimums:
        if tuple(int(n) for n in version.split('.')[:2]) > (14,0):
            raise RuntimeError(f'native file exceeds macOS14 target: {p}: {version}')
    relative = p.relative_to(app).parts
    executable = app / 'caliper-harness'
    if relative[0] == 'runtime':
        executable = app / 'runtime' / relative[1] / 'bin' / 'python3'
    elif p == app / 'bin' / 'rg':
        executable = p
    context = app / 'runtime' / relative[1] if relative[0] == 'runtime' else app
    search = inherited_paths(p, executable, context) + rpaths(executable, executable)
    for dep in metadata[p]:
        if apple(dep):
            continue
        if dep.startswith('@rpath/'):
            candidates = [(base / dep[len('@rpath/'):]).resolve() for base in search]
        else:
            candidates = [expand(dep, p, executable)]
        found = any(apple(q) or (q.is_relative_to(app) and q.is_file()) for q in candidates)
        if not found:
            raise RuntimeError(f'unresolved or external native dependency: {p}: {dep}; searched {candidates}')
    subprocess.run(['codesign', '--force', '--sign', '-', str(p)], check=True, capture_output=True)
    subprocess.run(['codesign', '--verify', '--strict', str(p)], check=True, capture_output=True)
print(f'Audited loader paths and ad-hoc signed {len(native)} native files; no public signing/notarization.')
