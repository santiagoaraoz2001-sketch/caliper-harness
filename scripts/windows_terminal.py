"""Raw-byte Windows ConPTY transport for the test observer (no model/runtime).

The pywinpty read API decodes each native chunk before callers see bytes. This
adapter keeps the ConPTY byte stream intact; consumers own incremental decoding.
Only the process created here is terminated. No console/process discovery.
"""
import ctypes as C
from ctypes import wintypes as W
import os
import subprocess
import threading
import time


class COORD(C.Structure):
    _fields_ = [('X', C.c_short), ('Y', C.c_short)]


class STARTUPINFO(C.Structure):
    _fields_ = [('cb', W.DWORD), ('lpReserved', W.LPWSTR), ('lpDesktop', W.LPWSTR),
                ('lpTitle', W.LPWSTR), ('dwX', W.DWORD), ('dwY', W.DWORD),
                ('dwXSize', W.DWORD), ('dwYSize', W.DWORD), ('dwXCountChars', W.DWORD),
                ('dwYCountChars', W.DWORD), ('dwFillAttribute', W.DWORD),
                ('dwFlags', W.DWORD), ('wShowWindow', W.WORD), ('cbReserved2', W.WORD),
                ('lpReserved2', C.c_void_p), ('hStdInput', W.HANDLE),
                ('hStdOutput', W.HANDLE), ('hStdError', W.HANDLE)]


class STARTUPINFOEX(C.Structure):
    _fields_ = [('StartupInfo', STARTUPINFO), ('lpAttributeList', C.c_void_p)]


class PROCESS_INFORMATION(C.Structure):
    _fields_ = [('hProcess', W.HANDLE), ('hThread', W.HANDLE),
                ('dwProcessId', W.DWORD), ('dwThreadId', W.DWORD)]


class RawConPTY:
    def __init__(self, argv, cwd, env, columns=120, rows=40):
        self.k = k = C.WinDLL('kernel32', use_last_error=True)
        self.handle = self.console = self.input = self.output = None
        self.pid = 0
        self._pending_output = bytearray()
        k.CloseHandle.argtypes = [W.HANDLE]
        k.CreatePipe.argtypes = [C.POINTER(W.HANDLE), C.POINTER(W.HANDLE), C.c_void_p, W.DWORD]
        k.CreatePseudoConsole.argtypes = [COORD, W.HANDLE, W.HANDLE, W.DWORD, C.POINTER(W.HANDLE)]
        k.CreatePseudoConsole.restype = C.c_long
        k.ClosePseudoConsole.argtypes = [W.HANDLE]
        k.InitializeProcThreadAttributeList.argtypes = [C.c_void_p, W.DWORD, W.DWORD, C.POINTER(C.c_size_t)]
        k.UpdateProcThreadAttribute.argtypes = [C.c_void_p, W.DWORD, C.c_size_t, C.c_void_p, C.c_size_t, C.c_void_p, C.c_void_p]
        k.DeleteProcThreadAttributeList.argtypes = [C.c_void_p]
        k.CreateProcessW.argtypes = [W.LPCWSTR, W.LPWSTR, C.c_void_p, C.c_void_p, W.BOOL, W.DWORD, C.c_void_p, W.LPCWSTR, C.POINTER(STARTUPINFOEX), C.POINTER(PROCESS_INFORMATION)]
        k.GetExitCodeProcess.argtypes = [W.HANDLE, C.POINTER(W.DWORD)]
        k.TerminateProcess.argtypes = [W.HANDLE, W.UINT]
        k.WaitForSingleObject.argtypes = [W.HANDLE, W.DWORD]
        k.PeekNamedPipe.argtypes = [W.HANDLE, C.c_void_p, W.DWORD, C.c_void_p, C.POINTER(W.DWORD), C.c_void_p]
        k.ReadFile.argtypes = [W.HANDLE, C.c_void_p, W.DWORD, C.POINTER(W.DWORD), C.c_void_p]
        k.WriteFile.argtypes = [W.HANDLE, C.c_void_p, W.DWORD, C.POINTER(W.DWORD), C.c_void_p]
        cin, hostin, hostout, cout = [W.HANDLE() for _ in range(4)]
        attrs = None
        attrs_initialized = False
        try:
            self._ok(k.CreatePipe(C.byref(cin), C.byref(hostin), None, 0))
            self.input = hostin
            self._ok(k.CreatePipe(C.byref(hostout), C.byref(cout), None, 0))
            self.output = hostout
            pc = W.HANDLE()
            hr = k.CreatePseudoConsole(COORD(columns, rows), cin, cout, 0, C.byref(pc))
            if hr < 0:
                raise OSError('CreatePseudoConsole failed: HRESULT %08x' % (hr & 0xffffffff))
            self.console = pc
            size = C.c_size_t()
            k.InitializeProcThreadAttributeList(None, 1, 0, C.byref(size))
            attrs = C.create_string_buffer(size.value)
            self._ok(k.InitializeProcThreadAttributeList(attrs, 1, 0, C.byref(size)))
            attrs_initialized = True
            self._ok(k.UpdateProcThreadAttribute(attrs, 0, 0x00020016, pc, C.sizeof(W.HANDLE), None, None))
            si = STARTUPINFOEX()
            si.StartupInfo.cb = C.sizeof(si)
            si.StartupInfo.dwFlags = 0x00000100  # do not inherit redirected observer std handles
            si.lpAttributeList = C.cast(attrs, C.c_void_p)
            pi = PROCESS_INFORMATION()
            command = C.create_unicode_buffer(subprocess.list2cmdline([str(v) for v in argv]))
            block = C.create_unicode_buffer('\0'.join(k + '=' + str(v) for k, v in sorted(env.items(), key=lambda pair: pair[0].upper())) + '\0\0')
            self._ok(k.CreateProcessW(None, command, None, None, False, 0x00080000 | 0x00000400, block, str(cwd), C.byref(si), C.byref(pi)))
            self.handle, self.pid = pi.hProcess, pi.dwProcessId
            k.CloseHandle(pi.hThread)
        except BaseException:
            self.close(force=True)
            raise
        finally:
            if attrs_initialized:
                k.DeleteProcThreadAttributeList(attrs)
            for h in (cin, cout):
                if h.value:
                    k.CloseHandle(h)

    @staticmethod
    def _ok(value):
        if not value:
            raise C.WinError(C.get_last_error())

    @property
    def exitstatus(self):
        code = W.DWORD()
        if not self.handle:
            return getattr(self, '_exitstatus', None)
        self._ok(self.k.GetExitCodeProcess(self.handle, C.byref(code)))
        return None if code.value == 259 else code.value

    def isalive(self):
        return self.exitstatus is None if self.handle else False

    def write(self, text):
        raw = text.encode('utf-8')
        errors = []
        def writer():
            remaining = raw
            try:
                while remaining:
                    done = W.DWORD()
                    self._ok(self.k.WriteFile(self.input, remaining, len(remaining), C.byref(done), None))
                    if not done.value:
                        raise OSError('owned terminal input write made no progress')
                    remaining = remaining[done.value:]
            except BaseException as error:
                errors.append(error)
        thread = threading.Thread(target=writer, daemon=True)
        thread.start()
        deadline = time.monotonic() + 15
        # WriteFile and the console's output pipe can each block the other.
        # Drain concurrently, retaining exact bytes for the ordinary reader.
        while thread.is_alive():
            self._pending_output.extend(self._read_pipe())
            if len(self._pending_output) > 4 * 1024 * 1024 or time.monotonic() > deadline:
                self.close(force=True)
                thread.join(1)
                raise TimeoutError('owned terminal input stalled or exceeded 4MiB pending output; owned process closed')
            thread.join(.005)
        if errors:
            raise errors[0]

    def read_available(self):
        if self._pending_output:
            chunk = bytes(self._pending_output[:65536])
            del self._pending_output[:65536]
            return chunk
        return self._read_pipe()

    def _read_pipe(self):
        if not self.output:
            return b''
        available = W.DWORD()
        if not self.k.PeekNamedPipe(self.output, None, 0, None, C.byref(available), None):
            if C.get_last_error() in (109, 232):
                return b''
            raise C.WinError(C.get_last_error())
        if not available.value:
            return b''
        buf = C.create_string_buffer(min(65536, available.value))
        done = W.DWORD()
        self._ok(self.k.ReadFile(self.output, buf, len(buf), C.byref(done), None))
        return buf.raw[:done.value]

    def terminate(self, force=False):
        if self.isalive():
            self._ok(self.k.TerminateProcess(self.handle, 1))
            if self.k.WaitForSingleObject(self.handle, 5000) != 0:
                raise TimeoutError('owned terminal process did not settle after termination')

    def close(self, force=False):
        if self.handle and force:
            self.terminate(force=True)
        # A full output pipe can otherwise deadlock synchronous ConPTY teardown
        # on older Windows. The owner has finished capturing before close().
        if self.output:
            self.k.CloseHandle(self.output)
            self.output = None
        if self.console:
            self.k.ClosePseudoConsole(self.console)
            self.console = None
        if self.handle:
            self._exitstatus = self.exitstatus
        for attr in ('input', 'output', 'handle'):
            value = getattr(self, attr)
            if value:
                self.k.CloseHandle(value)
                setattr(self, attr, None)
