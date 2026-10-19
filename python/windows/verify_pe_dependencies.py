"""Builder gate: inspect static PE imports without trusting the host's DLL search."""
import json
import os
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


def inspect(app, objdump, entry='caliper.exe', system=None, verified_crt=()):
    # Local cross-platform PE tests supply their baseline explicitly; the
    # native release gate always obtains the real Windows registry baseline.
    if system is None:
        system = system_dlls()
    verified_crt = set(verified_crt)
    system = set(system) | verified_crt
    local = {p.name.lower(): p for p in app.iterdir() if p.is_file()}
    for name in local:
        if re.match(r'^(msvcp|vcruntime|concrt|vcomp|vccorlib)\d.*\.dll$', name):
            raise RuntimeError(f'Microsoft runtime must be installed separately: {name}')
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
            elif ((re.match(r'^(msvcp|vcruntime|concrt|vcomp|vccorlib)\d', key) and key not in verified_crt)
                  or (key not in system and not key.startswith(('api-ms-win-', 'ext-ms-win-')))):
                raise RuntimeError(f'{name} imports unbundled {dependency}; host-installed DLLs are not release dependencies')
    return report


if __name__ == '__main__':
    app = Path(sys.argv[1]).resolve(strict=True)
    powershell = Path(os.environ['SystemRoot']) / 'System32/WindowsPowerShell/v1.0/powershell.exe'
    runtime = json.loads(subprocess.check_output([
        str(powershell), '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File',
        str(app / 'ensure-windows-runtime.ps1'), '-CheckOnly', '-Json'], text=True))
    if not runtime['ready'] or not all(file['ready'] for file in runtime['files']):
        raise RuntimeError('System CRT verification failed')
    imports = inspect(app, sys.argv[2], verified_crt=[file['name'] for file in runtime['files']])
    print(json.dumps({'imports': imports, 'system_vc_runtime': runtime}, indent=2))
