package tui

import (
	"archive/zip"
	"io"
)

func newZipWriter(w io.Writer) *zip.Writer { return zip.NewWriter(w) }
