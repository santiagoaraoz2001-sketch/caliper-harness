"""Portable fixtures; native Windows import qualification remains a CI gate."""
import base64
import csv
import hashlib
import importlib.util
import io
from pathlib import Path
import struct
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('system_runtime', Path(__file__).with_name('use_system_runtime.py'))
runtime = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runtime)
MANGLED = 'msvcp140-a4c2229bdc2a2a630acdc095b4d86008.dll'


def pe(name=MANGLED, kind=1, bits=64):
    data = bytearray(2048)
    def put(fmt, offset, *values):
        struct.pack_into(fmt, data, offset, *values)
    data[:2] = b'MZ'
    put('<I', 60, 128)
    data[128:132] = b'PE\0\0'
    put('<H', 134, 1)
    opt = 152
    opt_size = 240 if bits == 64 else 224
    put('<H', 148, opt_size)
    put('<H', opt, 0x20b if bits == 64 else 0x10b)
    put('<I', opt + 60, 512)
    directory = opt + (112 if bits == 64 else 96)
    put('<I', directory - 4, 16)
    put('<IIII', opt + opt_size + 8, 1536, 4096, 1536, 512)
    size = {1:40, 13:64, 11:16}[kind]
    put('<II', directory + kind * 8, 4096, size)
    if kind == 1:
        put('<IIIII', 512, 0, 123, 0, 4352, 0)
    elif kind == 13:
        put('<IIIIIIII', 512, 1, 4352, 0, 0, 0, 0, 0, 123)
    else:
        put('<IHH', 512, 123, 256, 0)
    data[768:768 + len(name)] = name.encode('ascii')
    return data


class NormalizationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def fixture(self, kind=1, bits=64):
        extension = self.root / 'extension.pyd'
        extension.write_bytes(pe(kind=kind, bits=bits))
        vendor = self.root / MANGLED
        vendor.write_bytes(b'vendored CRT fixture')
        return extension, vendor

    def test_all_import_tables_and_architectures_are_offset_preserving(self):
        for kind in (1, 13, 11):
            for bits in (32, 64):
                with self.subTest(kind=kind, bits=bits):
                    path, vendor = self.fixture(kind, bits)
                    before = path.read_bytes()
                    report = runtime.normalize(self.root)
                    after = path.read_bytes()
                    entries, _ = runtime.imports(after)
                    self.assertEqual(entries[0][1], 'msvcp140.dll')
                    self.assertEqual(entries[0][0], 768)
                    self.assertEqual(len(before), len(after))
                    self.assertEqual(after[900:], before[900:])
                    self.assertFalse(vendor.exists())
                    self.assertEqual(len(report['rewritten_imports']), 1)
                    self.assertEqual(runtime.normalize(self.root), {'rewritten_imports': [], 'removed_dlls': []})

    def test_canonical_import_and_non_microsoft_library_remain(self):
        path = self.root / 'extension.pyd'
        path.write_bytes(pe('libopenblas-hash.dll'))
        before = path.read_bytes()
        other = self.root / 'libopenblas-hash.dll'
        other.write_bytes(pe('VCRUNTIME140.dll'))
        vendor = self.root / 'VCRUNTIME140.dll'
        vendor.write_bytes(b'CRT fixture')
        runtime.normalize(self.root)
        self.assertEqual(path.read_bytes(), before)
        self.assertTrue(other.exists())
        self.assertFalse(vendor.exists())

    def test_dynamic_ascii_and_wide_names_refuse_without_any_effect(self):
        for wide in (False, True):
            with self.subTest(wide=wide):
                path, vendor = self.fixture()
                before = path.read_bytes()
                source = self.root / 'loader.py'
                source.write_bytes(MANGLED.encode('utf-16le' if wide else 'ascii'))
                with self.assertRaisesRegex(ValueError, 'unresolved mangled'):
                    runtime.normalize(self.root)
                self.assertEqual(path.read_bytes(), before)
                self.assertTrue(vendor.exists())

    def test_arbitrary_pe_string_is_not_rewritten(self):
        path, vendor = self.fixture()
        raw = bytearray(path.read_bytes())
        raw[1000:1000 + len(MANGLED)] = MANGLED.encode('ascii')
        path.write_bytes(raw)
        with self.assertRaisesRegex(ValueError, 'unresolved mangled'):
            runtime.normalize(self.root)
        self.assertEqual(path.read_bytes(), raw)
        self.assertTrue(vendor.exists())

    def test_malformed_pe_and_rva_refuse_before_deletion(self):
        for malformed in (b'MZ', b'not PE', pe()[:550]):
            path, vendor = self.fixture()
            path.write_bytes(malformed)
            with self.assertRaises(ValueError):
                runtime.normalize(self.root)
            self.assertTrue(vendor.exists())
        malformed = pe()
        struct.pack_into('<I', malformed, 524, 0xffffff00)
        path.write_bytes(malformed)
        with self.assertRaisesRegex(ValueError, 'RVA'):
            runtime.normalize(self.root)
        self.assertTrue(vendor.exists())

    def test_record_removes_dll_and_updates_modified_pe_hash(self):
        site = self.root / 'Lib' / 'site-packages'
        site.mkdir(parents=True)
        extension = site / 'extension.pyd'
        extension.write_bytes(pe())
        vendor = site / MANGLED
        vendor.write_bytes(b'CRT fixture')
        meta = site / 'numpy.dist-info'
        meta.mkdir()
        record = meta / 'RECORD'
        record.write_text(f'extension.pyd,sha256=old,2048\n{MANGLED},sha256=old,11\nnumpy.dist-info/RECORD,,\n')
        runtime.normalize(self.root)
        rows = list(csv.reader(io.StringIO(record.read_text())))
        digest = base64.urlsafe_b64encode(hashlib.sha256(extension.read_bytes()).digest()).rstrip(b'=').decode()
        self.assertEqual(rows, [['extension.pyd', 'sha256=' + digest, '2048'], ['numpy.dist-info/RECORD', '', '']])
        prior = record.read_bytes()
        runtime.normalize(self.root)
        self.assertEqual(record.read_bytes(), prior)

    def test_symlink_and_record_escape_are_refused(self):
        _, vendor = self.fixture()
        link = self.root / 'alias'
        link.symlink_to(vendor)
        with self.assertRaisesRegex(ValueError, 'symbolic'):
            runtime.normalize(self.root)
        link.unlink()
        info = self.root / 'a.dist-info'
        info.mkdir()
        (info / 'RECORD').write_text('../outside,,\n')
        with self.assertRaisesRegex(ValueError, 'escapes'):
            runtime.normalize(self.root)
        self.assertTrue(vendor.exists())


if __name__ == '__main__':
    unittest.main()
