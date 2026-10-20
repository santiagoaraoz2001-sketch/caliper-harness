package ask

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// cancelreader opens CONIN$ for Windows stdin, even when stdin is a pipe.
// Own a duplicate instead: Go's pipe Close cancels its pending ReadFile without
// closing the caller's handle. Input's existing cancellation waits for Close.
func ownedPipeInput(in *os.File) (*Input, error) {
	process := windows.CurrentProcess()
	var handle windows.Handle
	if err := windows.DuplicateHandle(process, windows.Handle(in.Fd()), process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, fmt.Errorf("cannot prepare owned pipe input: %w", err)
	}
	return NewInput(os.NewFile(uintptr(handle), in.Name())), nil
}
