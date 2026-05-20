package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func newRecoveryKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return strings.ReplaceAll(time.Now().UTC().Format("20060102150405.000000000"), ".", "")
}

func (rt *RuntimeSession) MarkAgentExitActivity() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.session.LastMode = SessionModeShell
	rt.session.UpdatedAt = time.Now().UTC()
	s := rt.session
	rt.mu.Unlock()
	if rt.manager != nil {
		_ = rt.manager.persist(context.Background(), s)
	}
}

func (rt *RuntimeSession) RecordShellCommandForRecovery(command string) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	rt.updateRecoveryFromSubmittedInputLocked(command)
	s := rt.session
	rt.mu.Unlock()
	if rt.manager != nil {
		_ = rt.manager.persist(context.Background(), s)
	}
}

func (rt *RuntimeSession) updateRecoveryFromSubmittedInputLocked(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.TrimSpace(rt.session.LastMode) == SessionModeAgent {
		if isAgentExitInput(text) {
			rt.session.LastMode = SessionModeShell
			rt.session.UpdatedAt = time.Now().UTC()
		}
		return
	}
	if strings.TrimSpace(rt.session.LastMode) == "" {
		rt.session.LastMode = SessionModeShell
	}
	if strings.TrimSpace(rt.session.LastCWD) == "" && rt.manager != nil {
		rt.session.LastCWD = rt.manager.defaultWorkingDir()
	}
	for _, line := range strings.Split(text, "\n") {
		for _, segment := range splitShellSegments(line) {
			rt.applyShellSegmentForRecoveryLocked(segment)
		}
	}
	rt.session.UpdatedAt = time.Now().UTC()
}

func (rt *RuntimeSession) applyShellSegmentForRecoveryLocked(segment string) {
	argv := shellFields(segment)
	if len(argv) == 0 {
		return
	}
	var envArgs []string
	for len(argv) > 0 && isShellEnvAssignment(argv[0]) {
		envArgs = append(envArgs, argv[0])
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return
	}
	cmd := shellCommandBase(argv[0])
	switch cmd {
	case "cd":
		rt.applyCDForRecoveryLocked(argv[1:])
	case "builtin", "command":
		if len(argv) > 1 && shellCommandBase(argv[1]) == "cd" {
			rt.applyCDForRecoveryLocked(argv[2:])
		}
	default:
		if info, ok := agentLaunchInfo(argv); ok {
			if len(envArgs) > 0 {
				info.ResumeCommand = strings.TrimSpace(joinEnvAssignments(envArgs) + " " + info.ResumeCommand)
			}
			rt.session.LastMode = SessionModeAgent
			rt.session.LastAgentKind = info.Kind
			rt.session.LastAgentStartCommand = strings.TrimSpace(segment)
			rt.session.LastAgentResumeCommand = info.ResumeCommand
			if info.Kind == "codex" && rt.manager != nil {
				rt.session.LastAgentHome = rt.manager.sessionCodexHome(rt.session)
			}
		}
	}
}

func (rt *RuntimeSession) applyCDForRecoveryLocked(args []string) {
	target := ""
	if len(args) == 0 {
		target = userHomeDir()
	} else {
		target = args[0]
	}
	current := strings.TrimSpace(rt.session.LastCWD)
	if current == "" && rt.manager != nil {
		current = rt.manager.defaultWorkingDir()
	}
	next, ok := resolveShellCWD(current, rt.session.LastPrevCWD, target)
	if !ok || next == "" {
		return
	}
	rt.session.LastPrevCWD = current
	rt.session.LastCWD = next
}

func splitShellSegments(line string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			b.WriteRune(r)
			continue
		}
		if r == ';' {
			flush()
			continue
		}
		if (r == '&' || r == '|') && i+1 < len(runes) && runes[i+1] == r {
			flush()
			i++
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return out
}

func shellFields(s string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func isShellEnvAssignment(arg string) bool {
	if strings.HasPrefix(arg, "-") {
		return false
	}
	i := strings.IndexByte(arg, '=')
	if i <= 0 {
		return false
	}
	name := arg[:i]
	for j, r := range name {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || j > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func shellCommandBase(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	return filepath.Base(cmd)
}

func resolveShellCWD(current, previous, target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" || target == "~" {
		return userHomeDir(), true
	}
	if target == "-" {
		if strings.TrimSpace(previous) == "" {
			return "", false
		}
		return previous, true
	}
	if strings.Contains(target, "$") {
		if target == "$HOME" || strings.HasPrefix(target, "$HOME/") {
			target = userHomeDir() + strings.TrimPrefix(target, "$HOME")
		} else if target == "${HOME}" || strings.HasPrefix(target, "${HOME}/") {
			target = userHomeDir() + strings.TrimPrefix(target, "${HOME}")
		} else {
			return "", false
		}
	}
	if strings.HasPrefix(target, "~/") {
		target = filepath.Join(userHomeDir(), strings.TrimPrefix(target, "~/"))
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), true
	}
	if strings.TrimSpace(current) == "" {
		current = userHomeDir()
	}
	return filepath.Clean(filepath.Join(current, target)), true
}

func userHomeDir() string {
	if dir, err := os.UserHomeDir(); err == nil && dir != "" {
		return dir
	}
	return "."
}

func isAgentExitInput(text string) bool {
	text = strings.TrimSpace(text)
	switch text {
	case "exit", "quit", "/exit", "/quit":
		return true
	default:
		return false
	}
}

type agentInfo struct {
	Kind          string
	ResumeCommand string
}

func agentLaunchInfo(argv []string) (agentInfo, bool) {
	if len(argv) == 0 {
		return agentInfo{}, false
	}
	cmd := shellCommandBase(argv[0])
	args := argv[1:]
	switch cmd {
	case "codex":
		return codexAgentInfo(argv[0], args)
	case "claude", "claude-code":
		return claudeAgentInfo(argv[0], args)
	case "gemini", "opencode", "aiden":
		return genericAgentInfo(cmd, argv[0], args)
	default:
		return agentInfo{}, false
	}
}

func codexAgentInfo(command string, args []string) (agentInfo, bool) {
	if hasAnyArg(args, "--version", "-V", "--help", "-h") {
		return agentInfo{}, false
	}
	sub := firstCodexSubcommand(args)
	switch sub {
	case "exec", "review", "login", "logout", "mcp", "plugin", "mcp-server", "app-server", "remote-control", "app", "completion", "update", "sandbox", "debug", "apply", "cloud", "exec-server", "features", "help":
		return agentInfo{}, false
	case "resume", "fork":
		return agentInfo{Kind: "codex", ResumeCommand: joinShellCommand(append([]string{command}, args...))}, true
	default:
		flags := preserveCLIFlags(args)
		resume := append([]string{command, "resume", "--last"}, flags...)
		return agentInfo{Kind: "codex", ResumeCommand: joinShellCommand(resume)}, true
	}
}

func firstCodexSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			if cliFlagTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

func claudeAgentInfo(command string, args []string) (agentInfo, bool) {
	if hasAnyArg(args, "--version", "-v", "--help", "-h") {
		return agentInfo{}, false
	}
	if hasAnyArg(args, "--resume", "--continue") {
		return agentInfo{Kind: "claude", ResumeCommand: joinShellCommand(append([]string{command}, args...))}, true
	}
	flags := preserveCLIFlags(args)
	resume := append([]string{command, "--continue"}, flags...)
	return agentInfo{Kind: "claude", ResumeCommand: joinShellCommand(resume)}, true
}

func genericAgentInfo(kind, command string, args []string) (agentInfo, bool) {
	if hasAnyArg(args, "--version", "-v", "-V", "--help", "-h") {
		return agentInfo{}, false
	}
	if hasResumeLikeArg(args) {
		return agentInfo{Kind: kind, ResumeCommand: joinShellCommand(append([]string{command}, args...))}, true
	}
	return agentInfo{Kind: kind, ResumeCommand: joinShellCommand(append([]string{command}, args...))}, true
}

func preserveCLIFlags(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}
		out = append(out, arg)
		if cliFlagTakesValue(arg) && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out
}

func cliFlagTakesValue(arg string) bool {
	switch arg {
	case "-c", "--config", "-i", "--image", "-m", "--model", "-p", "--profile", "-s", "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval", "--local-provider", "--remote", "--remote-auth-token-env":
		return true
	default:
		return false
	}
}

func hasAnyArg(args []string, values ...string) bool {
	want := map[string]bool{}
	for _, v := range values {
		want[v] = true
	}
	for _, arg := range args {
		if want[arg] {
			return true
		}
	}
	return false
}

func hasResumeLikeArg(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "resume", "--resume", "--continue", "continue", "--last":
			return true
		}
	}
	return false
}

func joinShellCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func joinEnvAssignments(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		i := strings.IndexByte(arg, '=')
		if i <= 0 {
			continue
		}
		parts = append(parts, arg[:i]+"="+shellQuote(arg[i+1:]))
	}
	return strings.Join(parts, " ")
}

func ensureCodexSessionHome(codexHome string) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		return err
	}
	source := sourceCodexHome(codexHome)
	for _, name := range []string{"auth.json", "config.toml", "skills", "plugins"} {
		if err := linkCodexHomeEntry(source, codexHome, name); err != nil {
			log.Printf("codex home link skipped name=%s: %v", name, err)
		}
	}
	return nil
}

func sourceCodexHome(target string) string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" && filepath.Clean(dir) != filepath.Clean(target) {
		return dir
	}
	return filepath.Join(userHomeDir(), ".codex")
}

func linkCodexHomeEntry(source, target, name string) error {
	src := filepath.Join(source, name)
	dst := filepath.Join(target, name)
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(src, dst); err == nil {
		return nil
	}
	if info, err := os.Stat(src); err == nil && !info.IsDir() {
		b, readErr := os.ReadFile(src)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dst, b, 0o600)
	}
	return nil
}
