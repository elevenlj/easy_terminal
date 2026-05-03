package session

import (
	"context"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type Terminal interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	Resize(cols, rows uint16) error
}

type Launcher interface {
	Launch(ctx context.Context) (ProcessHandle, error)
}

type ProcessHandle interface {
	Terminal() Terminal
	Process() Waiter
}

type Waiter interface {
	Wait() error
}

type ShellLauncher struct {
	Command string
	Args    []string
	Dir     string
}

func (l ShellLauncher) Launch(ctx context.Context) (ProcessHandle, error) {
	command := l.Command
	if command == "" {
		command = "/bin/zsh"
	}
	args := l.Args
	if len(args) == 0 {
		args = []string{"-i"}
	}
	dir := l.Dir
	if dir == "" {
		dir = os.Getenv("TERMINAL_WORKING_DIR")
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 60, Cols: 240})
	if err != nil {
		return nil, err
	}
	return &shellHandle{term: &ptyTerminal{file: f}, process: cmd}, nil
}

type shellHandle struct {
	term    Terminal
	process *exec.Cmd
}

func (h *shellHandle) Terminal() Terminal { return h.term }
func (h *shellHandle) Process() Waiter    { return h.process }

type ptyTerminal struct {
	file *os.File
}

func (t *ptyTerminal) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *ptyTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }
func (t *ptyTerminal) Close() error                { return t.file.Close() }
func (t *ptyTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

type ScreenLauncher struct {
	SessionName string
	Command     string
	Args        []string
	Dir         string
}

func (l ScreenLauncher) Launch(ctx context.Context) (ProcessHandle, error) {
	name := l.SessionName
	if name == "" {
		name = "easy-terminal"
	}
	command := l.Command
	if command == "" {
		command = "/bin/zsh"
	}
	args := []string{"-S", name, "-dm", command}
	args = append(args, l.Args...)
	cmd := exec.CommandContext(ctx, "screen", args...)
	if l.Dir != "" {
		cmd.Dir = l.Dir
	}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return &screenHandle{term: screenTerminal{name: name}, process: screenWaiter{}}, nil
}

type screenHandle struct {
	term    Terminal
	process Waiter
}

func (h *screenHandle) Terminal() Terminal { return h.term }
func (h *screenHandle) Process() Waiter    { return h.process }

type screenTerminal struct {
	name string
}

func (t screenTerminal) Read([]byte) (int, error) {
	return 0, os.ErrInvalid
}

func (t screenTerminal) Write(p []byte) (int, error) {
	cmd := exec.Command("screen", "-S", t.name, "-X", "stuff", string(p))
	if err := cmd.Run(); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t screenTerminal) Close() error {
	return exec.Command("screen", "-S", t.name, "-X", "quit").Run()
}

func (t screenTerminal) Resize(cols, rows uint16) error {
	return nil
}

type screenWaiter struct{}

func (screenWaiter) Wait() error { return nil }
