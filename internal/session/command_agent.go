package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type CommandAgentConfig struct {
	Enabled bool   `json:"enabled"`
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
}

func LoadCommandAgentConfig(path string) (*CommandAgentConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &CommandAgentConfig{}, nil
		}
		return nil, err
	}
	var cfg CommandAgentConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

type CommandAgent struct {
	cfg CommandAgentConfig
}

func NewCommandAgent(cfg *CommandAgentConfig) *CommandAgent {
	if cfg == nil {
		cfg = &CommandAgentConfig{}
	}
	return &CommandAgent{cfg: *cfg}
}

func (a *CommandAgent) Enabled() bool {
	return a != nil && a.cfg.Enabled && strings.TrimSpace(a.cfg.Agent) != ""
}

func (a *CommandAgent) Translate(ctx context.Context, request string) (string, error) {
	if !a.Enabled() {
		return "", errors.New("command agent is disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	prompt := strings.TrimSpace(a.cfg.Prompt)
	input := request
	if prompt != "" {
		input = prompt + "\n\n用户请求: " + request
	}
	cmd := exec.CommandContext(ctx, a.cfg.Agent)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	out := cleanAgentCommand(stdout.String())
	if out == "" {
		return "", errors.New("command agent returned empty command")
	}
	if looksDestructive(out) {
		return "", errors.New("command agent returned a dangerous command")
	}
	return out, nil
}

func cleanAgentCommand(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```sh")
	s = strings.TrimPrefix(s, "```shell")
	s = strings.TrimPrefix(s, "```bash")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func looksDestructive(cmd string) bool {
	lower := strings.ToLower(cmd)
	bad := []string{"rm -rf /", "mkfs", ":(){", "dd if=", ">/dev/sd", "diskutil erase", "shutdown", "reboot"}
	for _, s := range bad {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func DefaultCommandAgentConfigPath() string {
	return filepath.Join("conf", "command_agent.json")
}
