package session

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	larkSessionNavigatorAction = "easy_terminal_session_nav"
	larkSessionNavigatorLimit  = 8
)

func larkSessionNavigatorCardContent(selectedSessionID string) (string, error) {
	items, selectedSessionID := defaultLarkMessageRegistry.navItems(selectedSessionID, larkSessionNavigatorLimit)
	var selected LarkSessionNavItem
	for _, item := range items {
		if item.SessionID == selectedSessionID {
			selected = item
			break
		}
	}
	if selected.SessionID == "" && len(items) > 0 {
		selected = items[0]
		selectedSessionID = selected.SessionID
	}

	elements := make([]map[string]any, 0, 4)
	if len(items) == 0 {
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "plain_text", "content": "暂无会话通知"},
		})
	} else {
		actions := make([]map[string]any, 0, len(items))
		for _, item := range items {
			label := item.Name
			if label == "" {
				label = item.SessionID
			}
			if item.SessionID == selectedSessionID {
				label = "● " + label
			}
			actions = append(actions, map[string]any{
				"tag":  "button",
				"text": map[string]any{"tag": "plain_text", "content": truncateNavigatorLabel(label)},
				"type": navigatorButtonType(item, selectedSessionID),
				"value": map[string]any{
					"action":     larkSessionNavigatorAction,
					"session_id": item.SessionID,
				},
			})
		}
		elements = append(elements, map[string]any{"tag": "action", "actions": actions})
		elements = append(elements, map[string]any{
			"tag": "note",
			"elements": []map[string]any{{
				"tag":     "plain_text",
				"content": navigatorMetaText(selected),
			}},
		})
		elements = append(elements, map[string]any{
			"tag":  "div",
			"text": map[string]any{"tag": "plain_text", "content": selected.Content},
		})
	}

	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true, "update_multi": true},
		"header": map[string]any{
			"template": "turquoise",
			"title":    map[string]any{"tag": "plain_text", "content": "easy_terminal 会话导航"},
		},
		"elements": elements,
	}
	b, err := json.Marshal(card)
	return string(b), err
}

func navigatorButtonType(item LarkSessionNavItem, selectedSessionID string) string {
	if item.SessionID == selectedSessionID {
		return "primary"
	}
	if item.Running {
		return "default"
	}
	return "primary"
}

func navigatorMetaText(item LarkSessionNavItem) string {
	if item.SessionID == "" {
		return ""
	}
	parts := []string{item.SessionID}
	if item.Running {
		parts = append(parts, "running")
	} else {
		parts = append(parts, "waiting")
	}
	if item.UpdateNo > 0 {
		parts = append(parts, fmt.Sprintf("已更新-%d", item.UpdateNo))
	}
	return strings.Join(parts, " · ")
}

func truncateNavigatorLabel(label string) string {
	const maxRunes = 18
	rs := []rune(strings.TrimSpace(label))
	if len(rs) <= maxRunes {
		return string(rs)
	}
	return string(rs[:maxRunes-1]) + "…"
}
