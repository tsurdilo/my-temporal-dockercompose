package archiver

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// GzipCompress compresses data using gzip (best-speed level).
func GzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("gzip compress: create writer: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return nil, fmt.Errorf("gzip compress: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip compress: close: %w", err)
	}
	return buf.Bytes(), nil
}

// GzipDecompress decompresses gzip-compressed data.
func GzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: open: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: read: %w", err)
	}
	return out, nil
}
