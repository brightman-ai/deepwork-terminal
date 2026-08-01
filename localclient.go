package terminal

import (
	"net"
	"net/http"
)

// requestIsSameMachine reports whether the HTTP client is running on THIS machine —
// the precondition for the two capabilities where "the bytes never leave the box" is
// the whole argument: reading the server's own system clipboard (clipboard_native.go)
// and lifting the network-motivated upload cap (upload_limit.go).
//
// It is deliberately NOT an authorization check. authWrap already decided who may call
// at all ("ALL requests require auth code — no exceptions, no heuristics"); this answers
// a different question — WHERE the caller is — and the answer only ever REMOVES a
// restriction that exists because of the network in between.
//
// Two rules, and the asymmetry between them is the point:
//
//  1. RemoteAddr must be a loopback address. RemoteAddr is the kernel's answer, not the
//     client's, so it cannot be spoofed by a header. X-Forwarded-For is never consulted
//     for this — gin's c.ClientIP() (which deepwork-pro's equivalent uses) DOES honour
//     forwarding headers, and that would let a remote caller claim to be local.
//
//  2. …and no proxy/tunnel marker may be present. cloudflared runs ON this machine and
//     dials the server from 127.0.0.1, so a public tunnel visitor arrives looking exactly
//     like a local one. The tunnel hop leaves fingerprints (CF-Connecting-IP / CF-Ray /
//     X-Forwarded-For), and reading them is safe HERE precisely because they can only
//     DOWNGRADE: forging one costs the caller the local privilege, never grants it. A
//     tunnel that someday stops setting any of them fails closed only in the sense that
//     a remote user would get the local cap — which is why rule 1 stays the primary gate
//     and this list stays conservative rather than clever.
func requestIsSameMachine(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, header := range proxyMarkerHeaders {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// proxyMarkerHeaders are set by something that FORWARDED this request. Any one of them
// means the loopback RemoteAddr belongs to the proxy, not to the human.
var proxyMarkerHeaders = []string{
	"X-Forwarded-For",
	"X-Real-Ip",
	"Forwarded",
	"CF-Connecting-IP",
	"CF-Ray",
}
