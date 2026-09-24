package ingest

import "bytes"

func newReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
