"""The active-runtime gate must preserve rollback backups during repair."""
import importlib.util
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest

spec = importlib.util.spec_from_file_location('verify_runtime', Path(__file__).with_name('verify_runtime.py'))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class RuntimeScopeTests(unittest.TestCase):
    def test_rollback_and_download_copies_are_not_active_dependencies(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory)
            for relative in ('.runtime-repair-123/previous-0/vcruntime140.dll',
                             '.runtime-build/python/msvcp140.dll'):
                file = app / relative
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_bytes(b'preserve rollback or downloaded source')
            verifier.verify_no_bundled_crt(app)
            self.assertEqual(len(list(app.rglob('*.dll'))), 2)

    def test_every_active_root_still_rejects_crt(self):
        for relative in ('VCRUNTIME140.DLL', 'bin/msvcp140.dll',
                         'runtime/decision/Lib/site-packages/numpy.libs/msvcp140-deadbeef.dll',
                         'runtime/decision/.runtime-repair-not-an-exemption/concrt140.dll',
                         'python/decision/vccorlib140.dll'):
            with self.subTest(relative=relative), tempfile.TemporaryDirectory() as directory:
                app = Path(directory)
                file = app / relative
                file.parent.mkdir(parents=True, exist_ok=True)
                file.write_bytes(b'active vendored CRT')
                with self.assertRaisesRegex(RuntimeError, 'must not be bundled'):
                    verifier.verify_no_bundled_crt(app)

    def test_final_packager_still_rejects_crt_in_any_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory)
            (app / 'runtime').mkdir()
            (app / 'runtime/manifest.json').write_text('{"format":1,"platform":"windows-amd64"}')
            (app / 'ensure-windows-runtime.ps1').write_text('fixture')
            backup = app / '.runtime-repair-backup/vcruntime140.dll'
            backup.parent.mkdir()
            backup.write_bytes(b'not allowed in the final release')
            packager = Path(__file__).resolve().parents[2] / 'scripts/package-release.py'
            result = subprocess.run([sys.executable, str(packager), str(app), '--version', '0.1.2', '--out', str(app / 'dist')], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('must be installed separately, not shipped', result.stderr)

    def test_non_microsoft_active_library_is_retained(self):
        with tempfile.TemporaryDirectory() as directory:
            app = Path(directory)
            file = app / 'runtime/decision/numpy.libs/libopenblas.dll'
            file.parent.mkdir(parents=True)
            file.write_bytes(b'needed numerical library')
            verifier.verify_no_bundled_crt(app)
            self.assertTrue(file.exists())


if __name__ == '__main__':
    unittest.main()
