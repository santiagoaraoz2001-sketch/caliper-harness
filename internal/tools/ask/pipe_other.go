//go:build !windows

package ask

import "os"

// Unix pipes use the existing pollable cancelreader path.
func ownedPipeInput(*os.File) (*Input, error) { return nil, nil }
