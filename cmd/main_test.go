package main

import "testing"

func TestEnvFallback(t *testing.T) {
	t.Setenv("EASY_TERMINAL_TEST_ENV", "")
	if got := env("EASY_TERMINAL_TEST_ENV", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
	t.Setenv("EASY_TERMINAL_TEST_ENV", "value")
	if got := env("EASY_TERMINAL_TEST_ENV", "fallback"); got != "value" {
		t.Fatalf("expected env value, got %q", got)
	}
}
