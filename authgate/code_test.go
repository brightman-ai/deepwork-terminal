package authgate

import "testing"

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"E3X1-M6T2": "E3X1M6T2",
		"e3x1m6t2":  "E3X1M6T2",
		"E3X1M6T2":  "E3X1M6T2",
		"e3x1-m6t2": "E3X1M6T2",
		" E3X1 M6T2 ": "E3X1M6T2", // stray spaces dropped
		"":          "",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodeMatches(t *testing.T) {
	const stored = "AB2C-D3EF"
	for _, ok := range []string{"AB2C-D3EF", "ab2c-d3ef", "AB2CD3EF", "ab2cd3ef", " Ab2c-D3ef "} {
		if !CodeMatches(ok, stored) {
			t.Errorf("CodeMatches(%q, %q) = false, want true", ok, stored)
		}
	}
	for _, bad := range []string{"AB2C-D3EE", "wrong", ""} {
		if CodeMatches(bad, stored) {
			t.Errorf("CodeMatches(%q, %q) = true, want false", bad, stored)
		}
	}
	// An empty/unset expected code must never be satisfiable.
	if CodeMatches("", "") || CodeMatches("anything", "") {
		t.Error("empty expected code must never match")
	}
}
