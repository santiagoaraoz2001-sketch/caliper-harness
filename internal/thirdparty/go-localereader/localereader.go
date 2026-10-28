// Package localereader preserves the API of github.com/mattn/go-localereader.
// Caliper input is UTF-8: Go's Windows console file reads already convert
// ReadConsoleW to UTF-8. The upstream ACP decoder re-decodes those bytes,
// corrupting punctuation, non-Latin scripts and emoji on the ANSI input path.
// This local adapter adds no transcoding, runtime dependency or model surface.
package localereader

import "io"

func NewReader(r io.Reader) io.Reader { return r }
func UTF8(b []byte) ([]byte, error)   { return b, nil }
