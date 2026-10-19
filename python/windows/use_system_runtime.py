"""Normalize owned Windows staging files to the installed Microsoft CRT.

Only PE import-name slots are rewritten, in place, to shorter canonical names.
No installed user environment is changed. The caller verifies the system CRT
prerequisite before invoking this helper and runs native import tests afterward.
"""
import base64
import csv
import hashlib
import io
import json
from pathlib import Path
import re
import struct
import sys

CRT = re.compile(r'^(?P<base>(?:msvcp|vcruntime|concrt|vccorlib|vcomp)\d+(?:_[a-z0-9_]+)?|ucrtbase)(?:-[0-9a-f]{8,})?\.dll$', re.I)
MANGLED = re.compile(rb'(?:msvcp|vcruntime|concrt|vccorlib|vcomp)\d+(?:_[a-z0-9_]+)?-[0-9a-f]{8,}\.dll', re.I)
WIDE_PREFIX = b'|'.join(name.encode('utf-16le') for name in ('msvcp', 'vcruntime', 'concrt', 'vccorlib', 'vcomp'))
MANGLED_WIDE = re.compile(b'(?:' + WIDE_PREFIX + rb')(?:[0-9]\x00)+(?:_\x00(?:[a-z0-9_]\x00)+)?-\x00(?:[0-9a-f]\x00){8,}\.\x00d\x00l\x00l\x00', re.I)


def has_mangled_reference(data):
    # Literal search uses the byte-buffer implementation instead of a casefolded
    # regex walk over large numerical libraries; match only candidate offsets.
    lower = data.lower()
    for name in ('msvcp', 'vcruntime', 'concrt', 'vccorlib', 'vcomp'):
        for prefix, pattern in ((name.encode('ascii'), MANGLED), (name.encode('utf-16le'), MANGLED_WIDE)):
            start = lower.find(prefix)
            while start >= 0:
                if pattern.match(lower, start):
                    return True
                start = lower.find(prefix, start + len(prefix))
    return False


def canonical(name):
    match = CRT.fullmatch(name)
    return match['base'].lower() + '.dll' if match else None


def imports(data):
    """Return file offsets of normal, delay, and bound PE import DLL names."""
    def read(fmt, off):
        size = struct.calcsize(fmt)
        if off < 0 or off + size > len(data):
            raise ValueError('truncated PE structure')
        return struct.unpack_from(fmt, data, off)

    if data[:2] != b'MZ':
        raise ValueError('missing DOS header')
    pe, = read('<I', 60)
    if data[pe:pe + 4] != b'PE\0\0':
        raise ValueError('missing PE header')
    sections, = read('<H', pe + 6)
    optional_size, = read('<H', pe + 20)
    opt = pe + 24
    magic, = read('<H', opt)
    if magic == 0x20b:
        directory, count_off = opt + 112, opt + 108
        image_base, = read('<Q', opt + 24)
    elif magic == 0x10b:
        directory, count_off = opt + 96, opt + 92
        image_base, = read('<I', opt + 28)
    else:
        raise ValueError('unsupported PE optional header')
    count, = read('<I', count_off)
    if directory + count * 8 > opt + optional_size:
        raise ValueError('PE directories exceed optional header')
    header_size, = read('<I', opt + 60)
    table = opt + optional_size
    segments = [read('<IIII', table + i * 40 + 8) for i in range(sections)]

    def offset(rva, size=1):
        if 0 <= rva < header_size and rva + size <= min(header_size, len(data)):
            return rva
        for virtual_size, address, raw_size, raw in segments:
            if address <= rva < address + max(virtual_size, raw_size):
                delta = rva - address
                if delta + size <= raw_size and raw + delta + size <= len(data):
                    return raw + delta
        raise ValueError('PE RVA outside file-backed section')

    def string_at(rva):
        pos = offset(rva)
        end = pos
        while offset(rva + end - pos) == end and data[end] != 0:
            end += 1
        try:
            return pos, bytes(data[pos:end]).decode('ascii')
        except UnicodeDecodeError as exc:
            raise ValueError('non-ASCII DLL import name') from exc

    result = []
    for index, width in ((1, 20), (13, 32), (11, 8)):
        if index >= count:
            continue
        rva, size = read('<II', directory + index * 8)
        if not rva and not size:
            continue
        if not rva or size < width:
            raise ValueError('invalid PE import directory')
        consumed = 0
        while consumed + width <= size:
            pos = offset(rva + consumed, width)
            entry = data[pos:pos + width]
            if not any(entry):
                break
            if index == 1:
                name_rva, = read('<I', pos + 12)
                stamp = pos + 4
            elif index == 13:
                attrs, name_rva = read('<II', pos)
                if attrs not in (0, 1):
                    raise ValueError('unknown delay-import attributes')
                if not attrs:
                    name_rva -= image_base
                stamp = pos + 28
            else:
                _, name_delta, forwarders = read('<IHH', pos)
                name_rva = rva + name_delta
                stamp = pos
                if forwarders:
                    raise ValueError('bound forwarder imports need explicit normalization support')
            name_pos, name = string_at(name_rva)
            result.append((name_pos, name, stamp))
            consumed += width
        else:
            raise ValueError('unterminated PE import directory')
    return result, opt + 64


def normalize(root):
    root = Path(root).resolve(strict=True)
    files = sorted(p for p in root.rglob('*') if p.is_file())
    if any(p.is_symlink() for p in root.rglob('*')):
        raise ValueError('owned staging tree must not contain symbolic links')
    removed, patches, report, records = [], {}, [], []
    # Validate the whole stage before any changes. A failed stage stays inactive.
    for path in files:
        if canonical(path.name):
            removed.append(path)
            continue
        if path.name == 'RECORD' and path.parent.name.endswith('.dist-info'):
            records.append(path)
            continue
        is_pe = path.suffix.lower() in ('.dll', '.pyd', '.exe')
        if not is_pe and path.suffix.lower() not in ('.py', '.pyc', '.txt', '.json', '.pth') and not path.name.startswith('.load-order'):
            continue
        data = path.read_bytes()
        changed = data
        if is_pe:
            entries, checksum = imports(data)
            for pos, name, stamp in entries:
                target = canonical(name)
                if target and target != name.lower():
                    if changed is data:
                        changed = bytearray(data)
                    replacement = target.encode('ascii')
                    changed[pos:pos + len(name)] = replacement.ljust(len(name), b'\0')
                    changed[stamp:stamp + 4] = b'\0' * 4
                    report.append({'file': str(path.relative_to(root)), 'from': name, 'to': target, 'offset': pos})
            if changed != data:
                changed[checksum:checksum + 4] = b'\0' * 4
                # Parse the result again; offsets and directory layout must survive.
                imports(changed)
                patches[path] = changed
        # Do not guess at dynamic LoadLibrary calls, generated loader lists, or
        # unrecognized embedded references. Explicitly refuse instead of deleting
        # a DLL a loader may still require. This scans, never rewrites, arbitrary bytes.
        if has_mangled_reference(changed):
            raise ValueError(f'unresolved mangled CRT reference in {path.relative_to(root)}')
    # Wheel RECORD is installation metadata, not a dynamic DLL loader. Keep
    # removed entries and modified PE hashes consistent with the staged files.
    for record in records:
        rows, updated = [], False
        for row in csv.reader(io.StringIO(record.read_text(encoding='utf-8'))):
            if len(row) != 3:
                raise ValueError(f'malformed wheel RECORD: {record}')
            target = (record.parent.parent / row[0]).resolve()
            if not target.is_relative_to(root):
                raise ValueError(f'wheel RECORD path escapes staging: {row[0]}')
            if target in removed:
                updated = True
                continue
            if target in patches:
                content = patches[target]
                digest = base64.urlsafe_b64encode(hashlib.sha256(content).digest()).rstrip(b'=')
                row[1:] = ['sha256=' + digest.decode('ascii'), str(len(content))]
                updated = True
            rows.append(row)
        if updated:
            output = io.StringIO(newline='')
            csv.writer(output, lineterminator='\n').writerows(rows)
            patches[record] = output.getvalue().encode('utf-8')
    for path, data in patches.items():
        path.write_bytes(data)
    for path in removed:
        path.unlink()
    return {'rewritten_imports': report, 'removed_dlls': [str(p.relative_to(root)) for p in removed]}


if __name__ == '__main__':
    try:
        if len(sys.argv) != 2:
            raise ValueError('usage: use_system_runtime.py OWNED_STAGE_ROOT')
        print(json.dumps(normalize(sys.argv[1]), indent=2))
    except (OSError, ValueError, struct.error) as exc:
        sys.exit(f'System CRT normalization failed: {exc}')
