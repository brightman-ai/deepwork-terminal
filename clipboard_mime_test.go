package terminal

import "testing"

// TestClipboardMIMEAllowsOfficeDocuments guards the docx/xlsx/pptx upload path.
// Regression: office MIMEs used to fall through isAllowedClipboardMIME → 400,
// so a docx pasted/picked in the CLI portal was rejected before reaching disk.
// deepwork does NO text extraction; it only needs the file to land in the
// session sandbox so the agent can read it from the injected path.
func TestClipboardMIMEAllowsOfficeDocuments(t *testing.T) {
	allowed := []string{
		"application/msword",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.ms-excel",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.ms-powerpoint",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		// Pre-existing allowances kept as guard.
		"image/png", "application/pdf", "text/plain", "application/zip",
		// Case-insensitivity guard.
		"APPLICATION/VND.OPENXMLFORMATS-OFFICEDOCUMENT.WORDPROCESSINGML.DOCUMENT",
	}
	for _, mime := range allowed {
		if !isAllowedClipboardMIME(mime) {
			t.Errorf("isAllowedClipboardMIME(%q) = false, want true", mime)
		}
	}

	rejected := []string{"application/x-executable", "video/mp4", ""}
	for _, mime := range rejected {
		if isAllowedClipboardMIME(mime) {
			t.Errorf("isAllowedClipboardMIME(%q) = true, want false", mime)
		}
	}
}

// TestExtFromMIMEOfficeDocuments ensures the MIME→ext fallback (used only when
// the upload has no original filename, e.g. a clipboard blob) yields the right
// office extension instead of the generic .bin.
func TestExtFromMIMEOfficeDocuments(t *testing.T) {
	cases := map[string]string{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   ".docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
		"application/msword":         ".doc",
		"application/vnd.ms-excel":   ".xls",
		"application/vnd.ms-powerpoint": ".ppt",
		"application/zip":            ".zip",
		"image/png":                  ".png",
		"application/octet-stream":   ".bin",
	}
	for mime, want := range cases {
		if got := extFromMIME(mime); got != want {
			t.Errorf("extFromMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}
