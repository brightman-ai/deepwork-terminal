package terminal

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRequestIsSameMachine pins the two rules that decide whether the network-shaped
// restrictions (upload cap, native clipboard) apply to a caller.
//
// The forwarding cases are the ones with teeth. deepwork-terminal is routinely published
// through a cloudflared tunnel, and cloudflared connects to it from 127.0.0.1 — so without
// rule 2 every tunnel visitor on the internet would be classified as sitting at this
// keyboard. The headers are trusted here ONLY because trusting them can lose the caller a
// privilege and never grant one: a forged X-Forwarded-For downgrades the forger.
func TestRequestIsSameMachine(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       bool
	}{
		{name: "loopback v4", remoteAddr: "127.0.0.1:54321", want: true},
		{name: "loopback v6", remoteAddr: "[::1]:54321", want: true},
		{name: "lan address", remoteAddr: "192.168.1.20:54321", want: false},
		{name: "public address", remoteAddr: "203.0.113.7:443", want: false},
		{name: "no port", remoteAddr: "127.0.0.1", want: true},
		{name: "unparseable", remoteAddr: "not-an-address", want: false},
		{
			name:       "tunnel visitor arrives from loopback",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"CF-Connecting-IP": "203.0.113.7"},
			want:       false,
		},
		{
			name:       "reverse proxy on this box",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{"X-Forwarded-For": "10.0.0.5"},
			want:       false,
		},
		{
			name:       "forwarding header cannot buy the privilege back",
			remoteAddr: "203.0.113.7:443",
			headers:    map[string]string{"X-Forwarded-For": "127.0.0.1"},
			want:       false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/files/upload-limit", nil)
			r.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			assert.Equal(t, tc.want, requestIsSameMachine(r))
		})
	}
}
