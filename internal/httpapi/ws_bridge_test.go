package httpapi

import "testing"

func TestFilterTerminalResponses(t *testing.T) {
	in := []byte("a\x1b[12;40Rb\x1b[?1;2cc\x1b[>0;276;0cd\x1bP1+r436f=76616c\x1b\\e\x1b]10;rgb:ffff/ffff/ffff\x07f")
	got := string(filterTerminalResponses(in))
	if got != "abcdef" {
		t.Fatalf("unexpected filtered data: %q", got)
	}
}

func TestFilterTerminalResponsesKeepsUserNavigationInput(t *testing.T) {
	in := []byte("a\x1b[A\x1b[B\x1b[Cb")
	got := string(filterTerminalResponses(in))
	if got != string(in) {
		t.Fatalf("user navigation input should be preserved, got %q", got)
	}
}
