package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectCodexTerminalAgentContext(t *testing.T) {
	snapshot := strings.Join([]string{
		"╭────────────────────────────╮",
		"│ >_ OpenAI Codex (v0.130.0) │",
		"│ model: gpt-5.6-terra xhigh fast │",
		"│ directory: ~/project/easy_terminal │",
		"╰────────────────────────────╯",
		"› explain the current diff",
	}, "\n")

	got := DetectCodexTerminalAgentContext(snapshot)
	if got == nil {
		t.Fatal("expected Codex agent context")
	}
	if got.Directory != "~/project/easy_terminal" || got.Model != "gpt-5.6-terra" || got.Reasoning != "Extra high" {
		t.Fatalf("context = %#v", got)
	}
}

func TestDetectCodexTerminalAgentContextRequiresCodexHeader(t *testing.T) {
	snapshot := "model: gpt-5.6-terra xhigh fast\ndirectory: ~/project/easy_terminal"
	if got := DetectCodexTerminalAgentContext(snapshot); got != nil {
		t.Fatalf("plain terminal content must not expose an agent context: %#v", got)
	}
}

func TestDetectCodexTerminalAgentContextFromCurrentStatusLine(t *testing.T) {
	snapshot := strings.Join([]string{
		"Codex is ready.",
		"gpt-5.6-terra high fast · ~/Easy_Terminal_Workspace/默认",
	}, "\n")

	got := DetectCodexTerminalAgentContext(snapshot)
	if got == nil {
		t.Fatal("expected Codex context from current status line")
	}
	if got.Directory != "~/Easy_Terminal_Workspace/默认" || got.Model != "gpt-5.6-terra" || got.Reasoning != "High" {
		t.Fatalf("context = %#v", got)
	}
}

func TestDetectCodexTerminalAgentContextIgnoresStaleScrollbackHeader(t *testing.T) {
	lines := []string{
		"│ >_ OpenAI Codex (v0.130.0) │",
		"│ model: gpt-5.6-terra xhigh fast │",
		"│ directory: ~/project/easy_terminal │",
	}
	for i := 0; i < terminalAgentContextTailLineLimit+1; i++ {
		lines = append(lines, "shell output")
	}
	lines = append(lines, "$ pwd", "/tmp")

	if got := DetectCodexTerminalAgentContext(strings.Join(lines, "\n")); got != nil {
		t.Fatalf("stale Codex scrollback must not expose an agent context: %#v", got)
	}
}

func TestLarkNotificationCardRendersAgentContextBeforeButtons(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "Codex",
		Content:   "任务已完成",
		AgentContext: &TerminalAgentContext{
			Directory: "~/project/easy_terminal",
			Model:     "gpt-5.6-terra",
			Reasoning: "Extra high",
		},
	}, "ou_1", false)
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		Body struct {
			Elements []map[string]any `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(content), &card); err != nil {
		t.Fatal(err)
	}
	contextIndex := -1
	buttonIndex := -1
	for i, element := range card.Body.Elements {
		if element["tag"] == "column_set" && buttonIndex < 0 {
			buttonIndex = i
		}
		if element["tag"] != "div" {
			continue
		}
		text, _ := element["text"].(map[string]any)
		if strings.Contains(text["content"].(string), "目录：~/project/easy_terminal") {
			contextIndex = i
		}
	}
	if contextIndex < 0 || buttonIndex < 0 || contextIndex >= buttonIndex {
		t.Fatalf("agent context should appear before buttons, context=%d buttons=%d elements=%#v", contextIndex, buttonIndex, card.Body.Elements)
	}
	if contextIndex == 0 || card.Body.Elements[contextIndex-1]["tag"] != "hr" {
		t.Fatalf("agent context should be separated from the reply by a divider, elements=%#v", card.Body.Elements)
	}
	contextText := card.Body.Elements[contextIndex]["text"].(map[string]any)["content"].(string)
	if contextText != "目录：~/project/easy_terminal · 模型：gpt-5.6-terra · Reasoning：Extra high" {
		t.Fatalf("context text = %q", contextText)
	}
}

func TestLarkNotificationCardOmitsAgentContextForTerminal(t *testing.T) {
	content, err := larkNotificationCardContent(WaitingNotification{
		SessionID: "sess-1",
		Name:      "终端",
		Content:   "$ pwd\n/tmp",
	}, "ou_1", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "目录：") || strings.Contains(content, "Reasoning：") {
		t.Fatalf("plain terminal card should not render an agent context: %s", content)
	}
}
