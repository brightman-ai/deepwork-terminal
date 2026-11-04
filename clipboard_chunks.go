package terminal

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Stream parts with at most one open file, including for many small network
// slices. The clipboard saver hashes and publishes this stream as usual.
type chunkSequenceReader struct {
	dir   string
	count int
	next  int
	file  *os.File
}

func (r *chunkSequenceReader) Read(p []byte) (int, error) {
	for {
		if r.file == nil {
			if r.next == r.count {
				return 0, io.EOF
			}
			file, err := os.Open(filepath.Join(r.dir, strconv.Itoa(r.next)+".part"))
			if err != nil {
				return 0, err
			}
			r.file = file
			r.next++
		}
		n, err := r.file.Read(p)
		if err == io.EOF {
			if closeErr := r.Close(); closeErr != nil {
				return n, closeErr
			}
			if n == 0 {
				continue
			}
			return n, nil
		}
		return n, err
	}
}

func (r *chunkSequenceReader) Close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
