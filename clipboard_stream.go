package terminal

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

const clipboardMaxBytes = 4 << 20

// Only OSC 52 writes are observed. Absolute daemon offsets identify events across
// server restarts and browser reconnects. Framing and limits survive chunk splits.
type clipboardScanner struct {
	state           int
	payload         []byte
	startHistorical bool
	offset          int64
	pasteMode       func(bool)
}

func (s *clipboardScanner) feed(data []byte, historical bool, emit func(string, int64, bool)) {
	for _, b := range data {
		s.offset++
		switch s.state {
		case 0:
			if b == 27 {
				s.state = 1
				s.startHistorical = historical
			}
		case 1:
			if b == ']' {
				s.state = 2
				s.payload = nil
			} else if b == '[' {
				s.state = 6
				s.payload = nil
			} else if b != 27 {
				s.state = 0
			}
		case 2:
			if b == 7 {
				s.finish(emit)
				s.state = 0
			} else if b == 27 {
				s.state = 3
			} else if len(s.payload) < ((clipboardMaxBytes+2)/3)*4+32 {
				s.payload = append(s.payload, b)
			} else {
				s.payload = nil
				s.state = 4
			}
		case 3:
			if b == '\\' {
				s.finish(emit)
				s.state = 0
			} else {
				s.payload = nil
				s.state = 0
			}
		case 4:
			if b == 7 {
				s.state = 0
			} else if b == 27 {
				s.state = 5
			}
		case 5:
			if b == '\\' || b == 7 {
				s.state = 0
			} else {
				s.state = 4
			}
		case 6:
			if b >= 0x40 && b <= 0x7e {
				if s.pasteMode != nil && (b == 'h' || b == 'l') && len(s.payload) > 0 && s.payload[0] == '?' {
					for _, mode := range strings.Split(string(s.payload[1:]), ";") {
						if mode == "2004" {
							s.pasteMode(b == 'h')
						}
					}
				}
				s.state = 0
				s.payload = nil
			} else if len(s.payload) < 64 {
				s.payload = append(s.payload, b)
			} else {
				s.state = 0
				s.payload = nil
			}
		}
	}
}
func (s *clipboardScanner) finish(emit func(string, int64, bool)) {
	payload := string(s.payload)
	s.payload = nil
	if !strings.HasPrefix(payload, "52;") {
		return
	}
	target, encoded, ok := strings.Cut(payload[3:], ";")
	if !ok || encoded == "?" || strings.Trim(target, "cps01234567") != "" {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) > clipboardMaxBytes || !utf8.Valid(raw) || len(raw) == 0 {
		return
	}
	emit(string(raw), s.offset, s.startHistorical)
}
