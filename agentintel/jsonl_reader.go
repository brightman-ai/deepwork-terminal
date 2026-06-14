package agentintel

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// JSONLReader reads JSONL files incrementally using offset tracking.
// Half-written lines (no trailing \n) are NOT consumed — they will be
// re-read on the next call once the line is complete. This prevents
// data loss when fsnotify fires mid-write.
// [Ref: TH-0501-m9j P1 fix, Codex review]
type JSONLReader struct {
	path   string
	offset int64
}

func NewJSONLReader(path string) *JSONLReader {
	return &JSONLReader{path: path}
}

// ReadNew reads only new lines since last read. Returns parsed JSON objects.
// If file was truncated (size < offset), resets offset to 0.
func (r *JSONLReader) ReadNew() ([]map[string]any, error) {
	var rows []map[string]any
	err := r.ReadNewFunc(func(row map[string]any) bool {
		rows = append(rows, row)
		return true
	})
	return rows, err
}

// ReadNewFunc calls fn for each new complete line. Stops if fn returns false.
// Incomplete lines (no trailing \n at EOF) are left unconsumed so they can
// be re-read when the writer finishes flushing.
func (r *JSONLReader) ReadNewFunc(fn func(row map[string]any) bool) error {
	f, err := os.Open(r.path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	// Reset if file was truncated.
	if info.Size() < r.offset {
		r.offset = 0
	}

	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return err
	}

	br := bufio.NewReaderSize(f, 1<<20)

	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				// Incomplete line (no trailing \n) — don't advance offset.
				// Will be re-read on next call once the writer finishes.
				break
			}
			return err
		}

		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) == 0 {
			r.offset += int64(len(line))
			continue
		}

		var row map[string]any
		if err := json.Unmarshal(trimmed, &row); err != nil {
			// Complete line (has \n) but malformed JSON — skip it.
			r.offset += int64(len(line))
			continue
		}

		r.offset += int64(len(line))
		if !fn(row) {
			break
		}
	}

	return nil
}

// Offset returns current read position.
func (r *JSONLReader) Offset() int64 { return r.offset }
