package terminal

import (
	"strings"

	"github.com/coder/websocket"
)

// Safari/Apple WebKit can advertise compression but reject compressed messages
// produced by this library, closing immediately after replay (coder/websocket#218).
// Keep compression for Chromium/Firefox, and use the interoperable uncompressed
// transport for Apple browsers, including iPad desktop mode and embedded iOS views.
func terminalWSCompression(userAgent string) websocket.CompressionMode {
	ua := strings.ToLower(userAgent)
	appleMobile := strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ipod")
	safari := strings.Contains(ua, "safari") && !strings.Contains(ua, "chrome") && !strings.Contains(ua, "chromium") && !strings.Contains(ua, "android")
	if appleMobile || safari {
		return websocket.CompressionDisabled
	}
	return websocket.CompressionNoContextTakeover
}
