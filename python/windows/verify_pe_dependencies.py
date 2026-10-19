"""Builder gate: inspect static PE imports without trusting the host's DLL search."""
import json
from pathlib import Path
import re
import subprocess
import sys


def system_dlls():
    # Windows owns this baseline. Searching System32 would incorrectly bless
    # compiler runtimes installed by unrelated software on a development host.
    import winreg
    with winreg.OpenKey(winreg.HKEY_LOCAL_MACHINE,
                        r'SYSTEM\CurrentControlSet\Control\Session Manager\KnownDLLs') as key:
        values = [winreg.EnumValue(key, i)[1] for i in range(winreg.QueryInfoKey(key)[1])]
    return {value.lower() for value in values
            if isinstance(value, str) and value.lower().endswith('.dll')}


def inspect(app, objdump, entry='caliper.exe', system=None):
    # Local cross-platform PE tests supply their baseline explicitly; the
    # native release gate always obtains the real Windows registry baseline.
    if system is None:
        system = system_dlls()
    local = {p.name.lower(): p for p in app.iterdir() if p.is_file()}
    pending = [entry.lower()]
    report = {}
    while pending:
        name = pending.pop()
        if name in report:
            continue
        result = subprocess.run([objdump, '-p', str(local[name])], text=True,
                                capture_output=True, check=True)
        imports = re.findall(r'DLL Name:\s*(\S+)', result.stdout)
        if not imports:
            raise RuntimeError(f'No PE imports found: {name}')
        report[name] = imports
        for dependency in imports:
            key = dependency.lower()
            if key in local:
                pending.append(key)
            elif (re.match(r'^(msvcp\d|vcruntime\d|concrt\d|lib.*\.dll$)', key)
                  or (key not in system and not key.startswith(('api-ms-win-', 'ext-ms-win-')))):
                raise RuntimeError(f'{name} imports unbundled {dependency}; host-installed DLLs are not release dependencies')
    return report


if __name__ == '__main__':
    print(json.dumps(inspect(Path(sys.argv[1]).resolve(strict=True), sys.argv[2]), indent=2))
