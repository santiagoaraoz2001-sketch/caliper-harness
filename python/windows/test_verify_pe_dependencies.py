"""Cross-platform import-audit fixtures; native import proof runs in release CI."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import verify_pe_dependencies as audit


class AuditTest(unittest.TestCase):
    def test_only_verified_system_crt_is_admitted(self):
        with tempfile.TemporaryDirectory() as folder:
            app = Path(folder)
            (app / 'caliper.exe').touch()
            output = 'DLL Name: KERNEL32.dll\nDLL Name: MSVCP140.dll\n'
            with patch.object(audit.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, output)):
                with self.assertRaisesRegex(RuntimeError, 'MSVCP140'):
                    audit.inspect(app, 'objdump', system={'kernel32.dll'})
                # An incidental registry entry does not substitute for verification.
                with self.assertRaisesRegex(RuntimeError, 'MSVCP140'):
                    audit.inspect(app, 'objdump', system={'kernel32.dll', 'msvcp140.dll'})
                report = audit.inspect(app, 'objdump', system={'kernel32.dll'}, verified_crt={'msvcp140.dll'})
                self.assertEqual(report['caliper.exe'], ['KERNEL32.dll', 'MSVCP140.dll'])

    def test_local_crt_and_unresolved_compiler_dependency_refuse(self):
        with tempfile.TemporaryDirectory() as folder:
            app = Path(folder)
            (app / 'caliper.exe').touch()
            dll = app / 'msvcp140.dll'
            dll.touch()
            with self.assertRaisesRegex(RuntimeError, 'installed separately'):
                audit.inspect(app, 'objdump', system=set())
            dll.unlink()
            output = 'DLL Name: libgcc_s_seh-1.dll\n'
            with patch.object(audit.subprocess, 'run', return_value=subprocess.CompletedProcess([], 0, output)):
                with self.assertRaisesRegex(RuntimeError, 'libgcc'):
                    audit.inspect(app, 'objdump', system=set(), verified_crt={'msvcp140.dll'})

    def test_packager_refuses_nested_runtime_and_installer(self):
        script = Path(__file__).resolve().parents[2] / 'scripts/package-release.py'
        import sys
        for name in ('runtime/decision/numpy.libs/msvcp140-hashed.dll', 'vc_redist.x64.exe'):
            with tempfile.TemporaryDirectory() as folder:
                app = Path(folder) / 'Caliper'
                (app / 'runtime').mkdir(parents=True)
                (app / 'runtime/manifest.json').write_text(json.dumps({'format': 1, 'platform': 'windows-amd64'}))
                (app / 'ensure-windows-runtime.ps1').touch()
                forbidden = app / name
                forbidden.parent.mkdir(parents=True, exist_ok=True)
                forbidden.touch()
                result = subprocess.run([sys.executable, str(script), str(app), '--version', '0.1.2', '--out', str(Path(folder) / 'dist')], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('must be installed separately', result.stderr)
                self.assertFalse((Path(folder) / 'dist').exists())


if __name__ == '__main__':
    unittest.main()
