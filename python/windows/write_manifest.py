"""Write the bundle's sha256 inventory without one process per Python file."""
from pathlib import Path
import hashlib
import os
import sys

root = Path(sys.argv[1]).resolve(strict=True)
pending = root / '.MANIFEST.sha256.tmp'
with pending.open('x') as output:
    for path in sorted(root.rglob('*')):
        if not path.is_file() or path.is_symlink() or path in (root / 'MANIFEST.sha256', pending):
            continue
        digest = hashlib.sha256()
        with path.open('rb') as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(chunk)
        output.write(f'{digest.hexdigest()}  ./{path.relative_to(root).as_posix()}\n')
    output.flush()
    os.fsync(output.fileno())
os.replace(pending, root / 'MANIFEST.sha256')
