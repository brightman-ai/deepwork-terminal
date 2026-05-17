package terminal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxInputSummaryCodes = 8
const maxOutputAfterSubmitLogs = 8
const outputAfterSubmitWindow = 2 * time.Second
const outputAfterSubmitMinLogInterval = 250 * time.Millisecond
const maxOutputWindowRunes = 4096

type terminalInputTracker struct {
	mu                sync.Mutex
	line              []rune
	framesSinceSubmit int
	bytesSinceSubmit  int
	submitSeq         int
	lastSubmittedLine string
	lastSubmitSeq     int
	lastSubmitAt      time.Time
	outputLogsLeft    int
	outputWindowText  string
	outputChunks      int
	outputBytes       int
	outputLastLogAt   time.Time
	outputOccurrences int
}

var terminalInputTrackers sync.Map

func observeTerminalInput(ctx context.Context, sessionID string, data []byte) {
	if len(data) == 0 {
		return
	}

	terminalInputFramesTotal.Inc()
	terminalInputBytesTotal.Add(uint64(len(data)))

	tracker := terminalInputTrackerFor(sessionID)
	tracker.observe(ctx, sessionID, data)
}

func observeTerminalOutput(ctx context.Context, sessionID string, data []byte) {
	if len(data) == 0 {
		return
	}

	terminalOutputFramesTotal.Inc()
	terminalOutputBytesTotal.Add(uint64(len(data)))

	value, ok := terminalInputTrackers.Load(sessionID)
	if !ok {
		return
	}
	value.(*terminalInputTracker).observeOutput(ctx, sessionID, data)
}

func clearTerminalInputTracker(sessionID string) {
	terminalInputTrackers.Delete(sessionID)
}

func terminalInputTrackerFor(sessionID string) *terminalInputTracker {
	if value, ok := terminalInputTrackers.Load(sessionID); ok {
		return value.(*terminalInputTracker)
	}
	tracker := &terminalInputTracker{}
	value, _ := terminalInputTrackers.LoadOrStore(sessionID, tracker)
	return value.(*terminalInputTracker)
}

func (t *terminalInputTracker) observe(ctx context.Context, sessionID string, data []byte) {
	text := string(data)
	t.mu.Lock()
	defer t.mu.Unlock()

	t.framesSinceSubmit++
	t.bytesSinceSubmit += len(data)

	if len(text) > 0 && text[0] == 0x1b {
		t.emitControl(ctx, sessionID, data, 0x1b)
		return
	}

	sawSubmit := false
	sawControl := false
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			t.emitSubmit(ctx, sessionID, data)
			sawSubmit = true
		case r == 0x7f:
			if len(t.line) > 0 {
				t.line = t.line[:len(t.line)-1]
			}
			sawControl = true
		case r > 0 && r < 0x20 && r != '\t':
			t.emitControl(ctx, sessionID, data, r)
			if r == 0x03 || r == 0x15 {
				t.resetLine()
			}
			sawControl = true
		default:
			t.line = append(t.line, r)
		}
	}

	if !sawSubmit && !sawControl && shouldLogInputTextFrame(text, data) {
		terminalLogger.Info(ctx, "cli input text frame",
			"session_id", sessionID,
			"data", summarizeInputBytes(data),
			"line", summarizeInputText(string(t.line)),
			"frames_since_submit", t.framesSinceSubmit,
			"bytes_since_submit", t.bytesSinceSubmit)
	}
}

func (t *terminalInputTracker) emitSubmit(ctx context.Context, sessionID string, data []byte) {
	t.submitSeq++
	line := string(t.line)
	now := time.Now()
	t.lastSubmittedLine = line
	t.lastSubmitSeq = t.submitSeq
	t.lastSubmitAt = now
	t.outputLogsLeft = maxOutputAfterSubmitLogs
	t.outputWindowText = ""
	t.outputChunks = 0
	t.outputBytes = 0
	t.outputLastLogAt = time.Time{}
	t.outputOccurrences = 0
	terminalInputSubmitsTotal.Inc()
	terminalLogger.Info(ctx, "cli input submit",
		"session_id", sessionID,
		"submit_seq", t.submitSeq,
		"line", summarizeInputText(line),
		"data", summarizeInputBytes(data),
		"frames_since_submit", t.framesSinceSubmit,
		"bytes_since_submit", t.bytesSinceSubmit,
		"server_ts_unix_ms", now.UnixMilli())
	t.resetLine()
}

func (t *terminalInputTracker) emitControl(ctx context.Context, sessionID string, data []byte, code rune) {
	terminalLogger.Info(ctx, "cli input control frame",
		"session_id", sessionID,
		"control_code", fmt.Sprintf("%x", code),
		"data", summarizeInputBytes(data),
		"line", summarizeInputText(string(t.line)),
		"frames_since_submit", t.framesSinceSubmit,
		"bytes_since_submit", t.bytesSinceSubmit)
}

func (t *terminalInputTracker) resetLine() {
	t.line = t.line[:0]
	t.framesSinceSubmit = 0
	t.bytesSinceSubmit = 0
}

func (t *terminalInputTracker) observeOutput(ctx context.Context, sessionID string, data []byte) {
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.lastSubmitAt.IsZero() || now.Sub(t.lastSubmitAt) > outputAfterSubmitWindow || t.outputLogsLeft <= 0 {
		return
	}

	t.outputChunks++
	t.outputBytes += len(data)
	chunkText := printableTerminalText(data)
	if chunkText != "" {
		t.outputWindowText = trimRunes(t.outputWindowText+chunkText, maxOutputWindowRunes)
	}
	line := t.lastSubmittedLine
	chunkOccurrences := 0
	windowOccurrences := 0
	if line != "" {
		chunkOccurrences = strings.Count(chunkText, line)
		windowOccurrences = strings.Count(t.outputWindowText, line)
	}
	reason := t.outputLogReason(now, chunkText, windowOccurrences)
	if reason == "" {
		return
	}

	t.outputLogsLeft--
	t.outputLastLogAt = now
	t.outputOccurrences = windowOccurrences
	terminalLogger.Info(ctx, "cli output after submit",
		"session_id", sessionID,
		"submit_seq", t.lastSubmitSeq,
		"reason", reason,
		"elapsed_ms", now.Sub(t.lastSubmitAt).Milliseconds(),
		"data", summarizeInputBytes(data),
		"submitted_line", summarizeInputText(line),
		"output_window", summarizeInputText(t.outputWindowText),
		"submitted_line_chunk_occurrences", chunkOccurrences,
		"submitted_line_window_occurrences", windowOccurrences,
		"chunks_since_submit", t.outputChunks,
		"bytes_since_submit", t.outputBytes,
		"logs_left", t.outputLogsLeft)
}

func (t *terminalInputTracker) outputLogReason(now time.Time, chunkText string, windowOccurrences int) string {
	if t.outputChunks == 1 {
		return "first"
	}
	if windowOccurrences != t.outputOccurrences {
		return "submitted_line_occurrence_changed"
	}
	if strings.ContainsAny(chunkText, "\r\n") {
		return "line_break"
	}
	if !t.outputLastLogAt.IsZero() && now.Sub(t.outputLastLogAt) >= outputAfterSubmitMinLogInterval {
		return "interval"
	}
	return ""
}

func shouldLogInputTextFrame(text string, data []byte) bool {
	if text == "" {
		return true
	}
	summary := summarizeInputText(text)
	return len(data) > 1 || summary["cjk"].(int) > 0
}

func printableTerminalText(data []byte) string {
	if !utf8.Valid(data) {
		return ""
	}
	return string(data)
}

func trimRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[len(runes)-limit:])
}

func summarizeInputBytes(data []byte) map[string]any {
	text := ""
	if utf8.Valid(data) {
		text = string(data)
	}
	summary := summarizeInputText(text)
	summary["bytes"] = len(data)
	return summary
}

func summarizeInputText(text string) map[string]any {
	codes := make([]string, 0, maxInputSummaryCodes)
	ascii := 0
	cjk := 0
	whitespace := 0
	length := 0

	for _, r := range text {
		length++
		if len(codes) < maxInputSummaryCodes {
			codes = append(codes, fmt.Sprintf("%x", r))
		}
		if r <= 0x7f {
			ascii++
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			whitespace++
		}
		if (r >= 0x3400 && r <= 0x9fff) || (r >= 0xf900 && r <= 0xfaff) {
			cjk++
		}
	}

	return map[string]any{
		"len":        length,
		"codes":      codes,
		"ascii":      ascii,
		"cjk":        cjk,
		"whitespace": whitespace,
	}
}
