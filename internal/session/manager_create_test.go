package session

import (
	"context"
	"testing"
)

func TestCreateSessionRunsPreStartCommand(t *testing.T) {
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher, WithPreStartCommand("source ~/.zshrc"))

	if _, err := manager.CreateSession(context.Background(), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if len(launcher.terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); got != "source ~/.zshrc\r" {
		t.Fatalf("pre-start write = %q, want command with carriage return", got)
	}
}

func TestCreateSessionSkipsEmptyPreStartCommand(t *testing.T) {
	launcher := &recordingLauncher{}
	manager := NewManager(nil, launcher, WithPreStartCommand("  "))

	if _, err := manager.CreateSession(context.Background(), "test"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if len(launcher.terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(launcher.terminals))
	}
	if got := launcher.terminals[0].writes(); got != "" {
		t.Fatalf("empty pre-start command should not write, got %q", got)
	}
}
