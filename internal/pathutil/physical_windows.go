package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// EvalSymlinks resolves both symbolic links and mount-point junctions. Since Go
// 1.23 filepath.EvalSymlinks does not follow junctions, so it cannot establish a
// physical workspace boundary on Windows. No directory enumeration is needed.
func EvalSymlinks(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	name, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "resolve physical path", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	buf := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", &os.PathError{Op: "resolve physical path", Path: path, Err: err}
		}
		if n >= uint32(len(buf)) {
			if n > 32768 {
				return "", fmt.Errorf("physical path exceeds Windows path limit")
			}
			buf = make([]uint16, n+1)
			continue
		}
		resolved := windows.UTF16ToString(buf[:n])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
		} else {
			resolved = strings.TrimPrefix(resolved, `\\?\`)
		}
		if !filepath.IsAbs(path) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			return filepath.Rel(cwd, resolved)
		}
		return resolved, nil
	}
}
