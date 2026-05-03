package httpapi

import "testing"

func TestFilterTerminalResponses(t *testing.T) {
	in := []byte("a\x1b[12;40Rb\x1b[?1;2cc\x1b]10;rgb:ffff/ffff/ffff\x07d")
	got := string(filterTerminalResponses(in))
	if got != "abcd" {
		t.Fatalf("unexpected filtered data: %q", got)
	}
}
