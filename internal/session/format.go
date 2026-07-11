package session

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

var emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

const (
	defaultMaxLarkTextLines     = 300
	maxLarkTextRunes            = 12000
	larkTruncatedPrefix         = "[truncated]\n"
	codexNoAnchorFallbackPrefix = "[missing input anchor; showing tail]\n"
)

var larkNotifyMaxLines atomic.Int64
var larkNotifyDropLinePatterns atomic.Value
var larkNotifyMergeWrappedLines atomic.Bool

type larkNotifyDropLinePattern struct {
	kind   string
	action string
	re     *regexp.Regexp
	groups []int
}

func init() {
	larkNotifyMaxLines.Store(defaultMaxLarkTextLines)
	larkNotifyDropLinePatterns.Store([]larkNotifyDropLinePattern{})
}

func SetLarkNotifyMaxLines(lines int) {
	if lines <= 0 {
		lines = defaultMaxLarkTextLines
	}
	larkNotifyMaxLines.Store(int64(lines))
}

func SetLarkNotifyMergeWrappedLines(enabled bool) {
	larkNotifyMergeWrappedLines.Store(enabled)
}

func SetLarkNotifyDropLinePatterns(patterns []string) error {
	rules := make([]LarkNotifyDropLineRule, 0, len(patterns))
	for _, pattern := range patterns {
		rules = append(rules, LarkNotifyDropLineRule{Pattern: pattern})
	}
	return SetLarkNotifyDropLineRules(rules)
}

func SetLarkNotifyDropLineRules(rules []LarkNotifyDropLineRule) error {
	compiled := make([]larkNotifyDropLinePattern, 0, len(rules))
	for _, rule := range rules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			continue
		}
		kind := normalizeLarkNotifyRuleKind(rule.Kind)
		if kind == "" {
			kind = "line"
		}
		if kind != "line" && kind != "block_head" && kind != "line_group" {
			return fmt.Errorf("invalid lark notify filter kind %q", rule.Kind)
		}
		action := normalizeLarkNotifyRuleAction(kind, rule.Action)
		if kind == "block_head" && action != "drop_block" && action != "keep_head" {
			return fmt.Errorf("invalid lark notify block filter action %q", rule.Action)
		}
		groups := normalizeLarkNotifyRuleGroups(rule.Groups)
		re, err := regexp.Compile(pattern)
		if err != nil {
			title := strings.TrimSpace(rule.Title)
			if title != "" {
				return fmt.Errorf("invalid lark notify drop line pattern %q (%s): %w", pattern, title, err)
			}
			return fmt.Errorf("invalid lark notify drop line pattern %q: %w", pattern, err)
		}
		if kind == "line_group" {
			if len(groups) == 0 {
				return fmt.Errorf("lark notify line group filter %q requires at least one capture group", pattern)
			}
			for _, group := range groups {
				if group > re.NumSubexp() {
					return fmt.Errorf("lark notify line group filter %q references missing capture group %d", pattern, group)
				}
			}
		}
		compiled = append(compiled, larkNotifyDropLinePattern{kind: kind, action: action, re: re, groups: groups})
	}
	larkNotifyDropLinePatterns.Store(compiled)
	return nil
}

func sanitizeForLarkAudit(text string) string {
	return emailRE.ReplaceAllString(text, "[email]")
}

func truncateForLark(text string) string {
	text = truncateLinesFromTail(text, int(larkNotifyMaxLines.Load()), "")
	return truncateRunesFromTail(text, maxLarkTextRunes, larkTruncatedPrefix)
}

func applyConfiguredLarkNotifyFilters(text string) string {
	patterns, _ := larkNotifyDropLinePatterns.Load().([]larkNotifyDropLinePattern)
	if len(patterns) == 0 || text == "" {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = applyConfiguredLarkNotifyBlockFilters(text, patterns)
	return applyConfiguredLarkNotifyLineFilters(text, patterns)
}

func applyConfiguredLarkNotifyBlockFilters(text string, patterns []larkNotifyDropLinePattern) string {
	hasBlockFilter := false
	for _, pattern := range patterns {
		if pattern.kind == "block_head" {
			hasBlockFilter = true
			break
		}
	}
	if !hasBlockFilter {
		return text
	}
	lines := strings.Split(text, "\n")
	blocks := splitLarkNotifyBlocks(lines)
	kept := make([]string, 0, len(lines))
	for _, block := range blocks {
		if len(block) == 0 {
			continue
		}
		action := ""
		for _, pattern := range patterns {
			if pattern.kind == "block_head" && pattern.re.MatchString(block[0]) {
				action = pattern.action
				break
			}
		}
		switch action {
		case "drop_block":
			continue
		case "keep_head":
			kept = append(kept, block[0])
		default:
			kept = append(kept, block...)
		}
	}
	return strings.Join(kept, "\n")
}

func splitLarkNotifyBlocks(lines []string) [][]string {
	blocks := make([][]string, 0, len(lines))
	var current []string
	currentUsesMarkerBoundary := false
	for _, line := range lines {
		markerBoundary := startsLarkNotifyMarkerBlock(line)
		startsNext := markerBoundary || (!currentUsesMarkerBoundary && startsLarkNotifyBlock(line))
		if startsNext && len(current) > 0 {
			blocks = append(blocks, current)
			current = nil
			currentUsesMarkerBoundary = markerBoundary
		} else if len(current) == 0 {
			currentUsesMarkerBoundary = markerBoundary
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	return blocks
}

func startsLarkNotifyMarkerBlock(line string) bool {
	trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
	if trimmed == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(trimmed)
	return r == '•' || r == '⏺'
}

func startsLarkNotifyBlock(line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	for _, r := range line {
		return !unicode.IsSpace(r)
	}
	return false
}

func applyConfiguredLarkNotifyLineFilters(text string, patterns []larkNotifyDropLinePattern) string {
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		drop := false
		for _, pattern := range patterns {
			switch pattern.kind {
			case "line":
				if pattern.re.MatchString(line) {
					drop = true
				}
			case "line_group":
				line = applyLarkNotifyLineGroupFilter(line, pattern)
			}
			if drop {
				break
			}
		}
		if !drop {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

type larkNotifyDropRange struct {
	start int
	end   int
}

func applyLarkNotifyLineGroupFilter(line string, pattern larkNotifyDropLinePattern) string {
	matches := pattern.re.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	ranges := make([]larkNotifyDropRange, 0, len(matches)*len(pattern.groups))
	for _, match := range matches {
		for _, group := range pattern.groups {
			index := group * 2
			if index+1 >= len(match) || match[index] < 0 || match[index+1] <= match[index] {
				continue
			}
			ranges = append(ranges, larkNotifyDropRange{start: match[index], end: match[index+1]})
		}
	}
	if len(ranges) == 0 {
		return line
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start == ranges[j].start {
			return ranges[i].end < ranges[j].end
		}
		return ranges[i].start < ranges[j].start
	})
	merged := ranges[:0]
	for _, item := range ranges {
		if len(merged) == 0 || item.start > merged[len(merged)-1].end {
			merged = append(merged, item)
			continue
		}
		if item.end > merged[len(merged)-1].end {
			merged[len(merged)-1].end = item.end
		}
	}
	var b strings.Builder
	start := 0
	for _, item := range merged {
		if item.start > start {
			b.WriteString(line[start:item.start])
		}
		start = item.end
	}
	if start < len(line) {
		b.WriteString(line[start:])
	}
	return b.String()
}

func mergeTerminalWrappedLinesForLark(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= 1 {
		return text
	}
	var b strings.Builder
	b.WriteString(lines[0])
	for i := 1; i < len(lines); i++ {
		if shouldMergeTerminalWrappedLineBreak(lines[i-1], lines[i]) {
			b.WriteString(lines[i])
			continue
		}
		b.WriteByte('\n')
		b.WriteString(lines[i])
	}
	return b.String()
}

func shouldMergeTerminalWrappedLineBreak(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftEdge, ok := lastBoundaryRune(left)
	if !ok || isLarkLineBreakSeparator(leftEdge) {
		return false
	}
	rightEdge, ok := firstBoundaryRune(right)
	if !ok || isLarkLineBreakSeparator(rightEdge) {
		return false
	}
	if startsWithOrderedListMarker(strings.TrimSpace(right)) {
		return false
	}
	return true
}

func lastBoundaryRune(text string) (rune, bool) {
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	if text == "" {
		return 0, false
	}
	var last rune
	for _, r := range text {
		last = r
	}
	return last, true
}

func firstBoundaryRune(text string) (rune, bool) {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, r := range text {
		return r, true
	}
	return 0, false
}

func isLarkLineBreakSeparator(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func startsWithOrderedListMarker(text string) bool {
	runes := []rune(text)
	if len(runes) >= 2 && isCJKListNumber(runes[0]) {
		switch runes[1] {
		case '、', '.', '．', ')', '）':
			return true
		}
	}
	digits := 0
	for _, r := range text {
		if !unicode.IsDigit(r) {
			return digits > 0 && (r == '.' || r == ')' || r == '、' || r == '．')
		}
		digits++
		if digits > 4 {
			return false
		}
	}
	return false
}

func isCJKListNumber(r rune) bool {
	return strings.ContainsRune("一二三四五六七八九十", r)
}

func truncateLinesFromTail(text string, maxLines int, prefix string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return prefix + strings.Join(lines[len(lines)-maxLines:], "\n")
}

func truncateRunesFromTail(text string, maxRunes int, prefix string) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	prefixRunes := []rune(prefix)
	keep := maxRunes - len(prefixRunes)
	if keep < 1 {
		return string(runes[len(runes)-maxRunes:])
	}
	return prefix + string(runes[len(runes)-keep:])
}

func StripTerminalControls(data []byte) string {
	var b strings.Builder
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch c {
		case 0x1b:
			i = skipEscape(data, i)
		case '\r':
			b.WriteByte('\n')
		case '\b':
			s := b.String()
			if len(s) > 0 {
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
		default:
			if c == '\n' || c == '\t' || (c >= 0x20 && c != 0x7f) {
				b.WriteByte(c)
			}
		}
	}
	return compactRepeatedLines(b.String())
}

func HasRenderableContent(data []byte) bool {
	text := StripTerminalControls(data)
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func PickNotifyContent(visibleSnapshot string, previousVisibleSnapshot string, roundReply []byte, lastInputText string) string {
	return pickNotifyContentWithWindow(visibleSnapshot, previousVisibleSnapshot, roundReply, lastInputText, "")
}

func pickNotifyContentWithWindow(visibleSnapshot string, previousVisibleSnapshot string, roundReply []byte, lastInputText string, windowStartInputText string) string {
	lastInputText = strings.TrimSpace(lastInputText)
	windowStartInputText = strings.TrimSpace(windowStartInputText)
	body, _ := selectNotifyBodyWithWindow(visibleSnapshot, previousVisibleSnapshot, roundReply, lastInputText, windowStartInputText)
	if body == "" {
		return ""
	}
	if isRawLarkNotifyInput(lastInputText) {
		return strings.TrimSpace(body)
	}
	body = trimVisibleText(body)
	body = dropCodexPromptStatusLines(body)
	body = applyConfiguredLarkNotifyFilters(body)
	body = trimVisibleText(body)
	return truncateForLark(sanitizeForLarkAudit(body))
}

func isRawLarkNotifyInput(input string) bool {
	input = strings.TrimSpace(input)
	return input == "/c" || strings.HasPrefix(input, "/c ")
}

func shouldPreservePreviousNotifyContent(previous string, current string) bool {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if previous == "" || current == "" || previous == RunningNotificationPlaceholder || current == RunningNotificationPlaceholder {
		return false
	}
	previousNorm := normalizedNotifyContentForRegression(previous)
	currentNorm := normalizedNotifyContentForRegression(current)
	if previousNorm == "" {
		return false
	}
	if currentNorm == "" {
		return true
	}
	previousRunes := []rune(previousNorm)
	currentRunes := []rune(currentNorm)
	if len(currentRunes) >= len(previousRunes) {
		return false
	}
	if strings.HasPrefix(previousNorm, currentNorm) {
		return len(currentRunes)*100 <= len(previousRunes)*95
	}
	common := commonPrefixRunes(previousNorm, currentNorm)
	return common >= 80 && common*100 >= len(currentRunes)*80 && len(currentRunes)*100 <= len(previousRunes)*85
}

func hasMeaningfulNotifyContent(text string) bool {
	return normalizedNotifyContentForRegression(text) != ""
}

func normalizedNotifyContentForRegression(text string) string {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isTransientStatusLine(trimmed) || isPromptStatusLine(trimmed) {
			continue
		}
		if text, ok := inputEchoText(trimmed); ok && strings.TrimSpace(text) == "" {
			continue
		}
		if text, ok := shellInputEchoText(trimmed); ok && strings.TrimSpace(text) == "" {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(strings.Fields(strings.Join(kept, "\n")), " ")
}

func commonPrefixRunes(left string, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	limit := len(leftRunes)
	if len(rightRunes) < limit {
		limit = len(rightRunes)
	}
	for i := 0; i < limit; i++ {
		if leftRunes[i] != rightRunes[i] {
			return i
		}
	}
	return limit
}

func NotifyContentNeedsMoreSnapshot(visibleSnapshot string, previousVisibleSnapshot string, roundReply []byte, lastInputText string) bool {
	return notifyContentNeedsMoreSnapshotWithWindow(visibleSnapshot, previousVisibleSnapshot, roundReply, lastInputText, "")
}

func notifyContentNeedsMoreSnapshotWithWindow(visibleSnapshot string, previousVisibleSnapshot string, roundReply []byte, lastInputText string, windowStartInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	windowStartInputText = strings.TrimSpace(windowStartInputText)
	body, _ := selectNotifyBodyWithWindow(visibleSnapshot, previousVisibleSnapshot, roundReply, lastInputText, windowStartInputText)
	if strings.TrimSpace(body) == "" {
		return true
	}
	if windowStartInputText != "" && lastInputText != "" && !containsInputEchoLine(trimVisibleText(body), lastInputText) {
		return true
	}
	hasReply := hasReplyLine(trimVisibleText(body), lastInputText)
	return !hasReply || (containsTransientStatusLine(body) && !hasReply)
}

func selectNotifyBody(visibleSnapshot string, previousVisibleSnapshot string, _ []byte, lastInputText string) (string, bool) {
	return selectNotifyBodyWithWindow(visibleSnapshot, previousVisibleSnapshot, nil, lastInputText, "")
}

func selectNotifyBodyWithWindow(visibleSnapshot string, previousVisibleSnapshot string, _ []byte, lastInputText string, windowStartInputText string) (string, bool) {
	if strings.TrimSpace(windowStartInputText) != "" {
		visibleBody, fromVisible := currentWindowVisibleText(visibleSnapshot, previousVisibleSnapshot, windowStartInputText)
		if strings.TrimSpace(visibleBody) != "" {
			return trimVisibleText(visibleBody), fromVisible
		}
	}
	visibleBody, fromVisible := currentRoundVisibleText(visibleSnapshot, previousVisibleSnapshot, lastInputText)
	if strings.TrimSpace(visibleBody) == "" {
		return "", false
	}
	return visibleBody, fromVisible
}

func NotifyContentNeedsConservativeDelay(visibleSnapshot string, previousVisibleSnapshot string, lastInputText string) bool {
	return notifyContentNeedsConservativeDelayWithWindow(visibleSnapshot, previousVisibleSnapshot, lastInputText, "")
}

func notifyContentNeedsConservativeDelayWithWindow(visibleSnapshot string, previousVisibleSnapshot string, lastInputText string, windowStartInputText string) bool {
	lastInputText = strings.TrimSpace(lastInputText)
	windowStartInputText = strings.TrimSpace(windowStartInputText)
	var body string
	var fromVisible bool
	if windowStartInputText != "" {
		body, fromVisible = currentWindowVisibleText(visibleSnapshot, previousVisibleSnapshot, windowStartInputText)
	} else {
		body, fromVisible = currentRoundVisibleText(visibleSnapshot, previousVisibleSnapshot, lastInputText)
	}
	if !fromVisible || strings.TrimSpace(body) == "" {
		return true
	}
	if windowStartInputText != "" && lastInputText != "" && !containsInputEchoLine(trimVisibleText(body), lastInputText) {
		return true
	}
	if containsTransientStatusLine(body) {
		return true
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isPromptStatusLine(trimmed) {
			return true
		}
		if isCodexSuggestionLine(trimmed) && !isInputEchoLine(trimmed, lastInputText) {
			return true
		}
	}
	return false
}

func currentWindowVisibleText(visibleSnapshot string, previousVisibleSnapshot string, windowStartInputText string) (string, bool) {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	if visibleSnapshot == "" {
		return "", false
	}
	if body, ok := visibleTextChangedSincePrevious(visibleSnapshot, previousVisibleSnapshot); ok && strings.TrimSpace(body) != "" {
		return trimVisibleText(body), true
	}
	if body, ok := visibleTextAfterPreviousTailAnchor(visibleSnapshot, previousVisibleSnapshot, 3); ok && strings.TrimSpace(body) != "" {
		return trimVisibleText(body), true
	}
	if body := visibleTextFromInputStart(visibleSnapshot, windowStartInputText); strings.TrimSpace(body) != "" {
		return trimVisibleText(body), true
	}
	return visibleSnapshot, true
}

func currentRoundVisibleText(visibleSnapshot string, previousVisibleSnapshot string, lastInputText string) (string, bool) {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	if visibleSnapshot == "" {
		return "", false
	}
	if strings.TrimSpace(lastInputText) != "" {
		if body := visibleTextFromLastInput(visibleSnapshot, lastInputText); strings.TrimSpace(body) != "" {
			return trimVisibleText(body), true
		}
	}
	if body, ok := visibleTextChangedSincePrevious(visibleSnapshot, previousVisibleSnapshot); ok {
		return trimVisibleText(body), true
	}
	if body, ok := visibleTextAfterPreviousTailAnchor(visibleSnapshot, previousVisibleSnapshot, 3); ok {
		return trimVisibleText(body), true
	}
	return visibleSnapshot, true
}

func visibleTextChangedSincePrevious(visibleSnapshot string, previousVisibleSnapshot string) (string, bool) {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	previousVisibleSnapshot = trimVisibleText(previousVisibleSnapshot)
	if visibleSnapshot == "" || previousVisibleSnapshot == "" {
		return "", false
	}
	currentLines := splitVisibleLines(visibleSnapshot)
	previousLines := splitVisibleLines(previousVisibleSnapshot)
	if len(currentLines) == 0 || len(previousLines) == 0 {
		return "", false
	}
	currentNorm := normalizedVisibleLines(currentLines)
	previousNorm := normalizedVisibleLines(previousLines)
	prefix := 0
	for prefix < len(currentNorm) && prefix < len(previousNorm) && currentNorm[prefix] == previousNorm[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(currentNorm)-prefix &&
		suffix < len(previousNorm)-prefix &&
		currentNorm[len(currentNorm)-1-suffix] == previousNorm[len(previousNorm)-1-suffix] {
		suffix++
	}
	if prefix == len(currentLines) && prefix == len(previousLines) {
		return "", true
	}
	start := prefix
	end := len(currentLines) - suffix
	for end < len(currentLines) && shouldKeepStableDiffSuffixLine(currentLines[end]) {
		end++
	}
	if start > end {
		start = end
	}
	if suffix == 0 {
		if anchored, ok := visibleTextAfterPreviousTailAnchor(visibleSnapshot, previousVisibleSnapshot, 3); ok && strings.TrimSpace(anchored) != "" {
			return anchored, true
		}
	}
	return strings.Join(currentLines[start:end], "\n"), true
}

func shouldKeepStableDiffSuffixLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if text, ok := shellInputEchoText(trimmed); ok && strings.TrimSpace(text) == "" {
		return true
	}
	if text, ok := inputEchoText(trimmed); ok && strings.TrimSpace(text) == "" {
		return true
	}
	return strings.Contains(lower, "press enter") ||
		strings.Contains(lower, "esc to go back") ||
		strings.Contains(lower, "enter to confirm")
}

func normalizedVisibleLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = normalizeVisibleAnchorLine(line)
	}
	return out
}

func visibleTextAfterPreviousTailAnchor(visibleSnapshot string, previousVisibleSnapshot string, anchorLines int) (string, bool) {
	previousAnchor := tailNonEmptyNormalizedLines(previousVisibleSnapshot, anchorLines)
	if len(previousAnchor) == 0 {
		return "", false
	}
	lines := splitVisibleLines(visibleSnapshot)
	type indexedLine struct {
		index int
		text  string
	}
	current := make([]indexedLine, 0, len(lines))
	for i, line := range lines {
		normalized := normalizeVisibleAnchorLine(line)
		if normalized != "" {
			current = append(current, indexedLine{index: i, text: normalized})
		}
	}
	if len(current) < len(previousAnchor) {
		return "", false
	}
	for i := len(current) - len(previousAnchor); i >= 0; i-- {
		matched := true
		for j := range previousAnchor {
			if current[i+j].text != previousAnchor[j] {
				matched = false
				break
			}
		}
		if matched {
			start := current[i+len(previousAnchor)-1].index + 1
			if start >= len(lines) {
				return "", true
			}
			return strings.Join(lines[start:], "\n"), true
		}
	}
	return "", false
}

func tailNonEmptyNormalizedLines(text string, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	lines := splitVisibleLines(trimVisibleText(text))
	out := make([]string, 0, maxLines)
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		normalized := normalizeVisibleAnchorLine(lines[i])
		if normalized == "" {
			continue
		}
		out = append(out, normalized)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func splitVisibleLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

func normalizeVisibleAnchorLine(line string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
}

func visibleTextAfterRoundStart(visibleSnapshot string, snapshotAtRoundStart string) string {
	visibleSnapshot = trimVisibleText(visibleSnapshot)
	snapshotAtRoundStart = trimVisibleText(snapshotAtRoundStart)
	if visibleSnapshot == "" || snapshotAtRoundStart == "" {
		return ""
	}
	if visibleSnapshot == snapshotAtRoundStart {
		return ""
	}
	if strings.HasPrefix(visibleSnapshot, snapshotAtRoundStart) {
		return trimVisibleText(strings.TrimPrefix(visibleSnapshot, snapshotAtRoundStart))
	}
	return ""
}

func trimVisibleText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func normalizeSnapshotText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func visibleTextFromLastInput(visibleSnapshot string, lastInputText string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if end, ok := inputAnchorEndLine(lines, i, lastInputText); ok {
			if end+1 >= len(lines) {
				return ""
			}
			return strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
		}
	}
	return ""
}

func visibleTextFromInputStart(visibleSnapshot string, inputText string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if _, ok := inputAnchorEndLine(lines, i, inputText); ok {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

func inputAnchorEndLine(lines []string, i int, lastInputText string) (int, bool) {
	if strings.TrimSpace(lastInputText) == "" {
		return i, false
	}
	if isStructuredInputAnchorLine(lines[i], lastInputText) {
		return i, true
	}
	if isInputEchoLine(lines[i], lastInputText) {
		return i, true
	}
	if end, ok := wrappedInputEchoEndAt(lines, i, lastInputText); ok {
		return end, true
	}
	return i, false
}

func visibleTextFromLastAnyInput(visibleSnapshot string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if text, ok := inputEchoText(lines[i]); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
		if text, ok := shellInputEchoText(lines[i]); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return ""
}

func isWrappedInputEchoAt(lines []string, i int, lastInputText string) bool {
	_, ok := wrappedInputEchoEndAt(lines, i, lastInputText)
	return ok
}

func wrappedInputEchoEndAt(lines []string, i int, lastInputText string) (int, bool) {
	text, ok := inputEchoText(lines[i])
	if !ok {
		return i, false
	}
	target := compactAnchorText(lastInputText)
	current := compactAnchorText(text)
	if target == "" || current == "" || !strings.HasPrefix(target, current) {
		return i, false
	}
	for j := i + 1; j < len(lines) && j <= i+6; j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			return i, false
		}
		if strings.HasPrefix(trimmed, "• ") || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			return i, false
		}
		current += compactAnchorText(trimmed)
		if current == target {
			return j, true
		}
		if !strings.HasPrefix(target, current) {
			return i, false
		}
	}
	return i, false
}

func compactAnchorText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), "")
}

func visibleTextFromLastShellInput(visibleSnapshot string) string {
	lines := strings.Split(visibleSnapshot, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		text, ok := shellInputEchoText(lines[i])
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		return strings.TrimSpace(strings.Join(lines[i:], "\n"))
	}
	return ""
}

func startsWithInputEcho(text string, input string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return isInputEchoLine(line, input)
	}
	return false
}

func inputEchoText(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if rest, ok := trimPromptPrefix(trimmed, "›"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefix(trimmed, ">"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefix(trimmed, "⏺"); ok {
		return unwrapAgentActionName(rest), true
	}
	for _, prompt := range []string{"%", "$", "#", ">"} {
		if rest, ok := trimPromptPrefix(trimmed, prompt); ok {
			return rest, true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(marker):]), true
		}
	}
	return "", false
}

func unwrapAgentActionName(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "("); idx > 0 && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(text[idx+1 : len(text)-1])
		if inner != "" {
			return inner
		}
	}
	return text
}

func isStructuredInputAnchorLine(line string, input string) bool {
	raw, ok := inputEchoTextRaw(line)
	if !ok || !strings.HasPrefix(raw, " ") {
		return false
	}
	return strings.TrimSpace(raw) == strings.TrimSpace(input)
}

func inputEchoTextRaw(line string) (string, bool) {
	trimmedLeft := strings.TrimLeft(line, " \t")
	if rest, ok := trimPromptPrefixRaw(trimmedLeft, "›"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefixRaw(trimmedLeft, ">"); ok {
		return rest, true
	}
	if rest, ok := trimPromptPrefixRaw(trimmedLeft, "⏺"); ok {
		return unwrapAgentActionName(rest), true
	}
	for _, prompt := range []string{"%", "$", "#", ">"} {
		if rest, ok := trimPromptPrefixRaw(trimmedLeft, prompt); ok {
			return rest, true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmedLeft, marker); idx >= 0 {
			return strings.TrimRight(trimmedLeft[idx+len(marker):], "\r\n"), true
		}
	}
	return "", false
}

func shellInputEchoText(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	for _, prompt := range []string{"%", "$", "#"} {
		if rest, ok := trimPromptPrefix(trimmed, prompt); ok {
			return rest, true
		}
		if strings.HasSuffix(trimmed, " "+prompt) {
			return "", true
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[idx+len(marker):]), true
		}
	}
	if strings.HasSuffix(trimmed, " >") {
		return "", true
	}
	marker := " > "
	if idx := strings.LastIndex(trimmed, marker); idx > 0 {
		return strings.TrimSpace(trimmed[idx+len(marker):]), true
	}
	return "", false
}

func trimPromptPrefix(line string, prompt string) (string, bool) {
	if line == prompt {
		return "", true
	}
	prefix := prompt + " "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
	}
	return "", false
}

func trimPromptPrefixRaw(line string, prompt string) (string, bool) {
	if line == prompt {
		return "", true
	}
	prefix := prompt + " "
	if strings.HasPrefix(line, prefix) {
		return strings.TrimRight(strings.TrimPrefix(line, prefix), "\r\n"), true
	}
	return "", false
}

func cleanupLarkNotifyText(text string, lastInputText string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if isTransientStatusLine(trimmed) {
			continue
		}
		if isPromptStatusLine(trimmed) {
			continue
		}
		if isPureHorizontalRuleLine(trimmed) {
			continue
		}
		if isCodexSuggestionLine(trimmed) && !isInputEchoLine(trimmed, lastInputText) {
			continue
		}
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func dropCodexPromptStatusLines(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if isPromptStatusLine(strings.TrimSpace(line)) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func restoreWrappedInputEcho(text string, lastInputText string) string {
	input := strings.TrimSpace(lastInputText)
	if strings.TrimSpace(text) == "" || input == "" {
		return text
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if restored, end, ok := restoredWrappedInputEchoAt(lines, i, input); ok {
			out = append(out, restored)
			i = end
			continue
		}
		out = append(out, lines[i])
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func restoredWrappedInputEchoAt(lines []string, i int, input string) (string, int, bool) {
	text, ok := inputEchoText(lines[i])
	if !ok {
		return "", i, false
	}
	target := compactAnchorText(input)
	current := compactAnchorText(text)
	if target == "" || current == "" || current == target || !strings.HasPrefix(target, current) {
		return "", i, false
	}
	for j := i + 1; j < len(lines) && j <= i+6; j++ {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			return "", i, false
		}
		if strings.HasPrefix(trimmed, "• ") || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			return "", i, false
		}
		current += compactAnchorText(trimmed)
		if current == target || strings.HasPrefix(current, target) {
			return inputEchoPrefix(lines[i]) + input, j, true
		}
		if !strings.HasPrefix(target, current) {
			return "", i, false
		}
	}
	return "", i, false
}

func inputEchoPrefix(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, prompt := range []string{"›", ">", "%", "$", "#"} {
		if trimmed == prompt {
			return prompt + " "
		}
		if strings.HasPrefix(trimmed, prompt+" ") {
			return prompt + " "
		}
		marker := " " + prompt + " "
		if idx := strings.LastIndex(trimmed, marker); idx >= 0 {
			return strings.TrimSpace(trimmed[:idx+len(marker)])
		}
	}
	return ""
}

func isPureHorizontalRuleLine(line string) bool {
	line = strings.TrimSpace(line)
	if len([]rune(line)) < 3 {
		return false
	}
	for _, r := range line {
		if unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '-', '_', '=', '─', '━', '—', '―', '═', '╌', '╍', '┄', '┅', '┈', '┉':
			continue
		default:
			return false
		}
	}
	return true
}

func containsTransientStatusLine(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if isTransientStatusLine(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func isTransientStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "working (") ||
		strings.Contains(lower, "esc to interrupt") ||
		(strings.Contains(lower, "background terminal") && strings.Contains(lower, "running")) ||
		strings.Contains(lower, "/ps to view") ||
		strings.Contains(lower, "/stop to close") ||
		strings.Contains(lower, "falling back from websockets") ||
		strings.Contains(lower, "stream disconnected before completion")
}

func hasReplyLine(text string, lastInputText string) bool {
	input := strings.TrimSpace(lastInputText)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := 0
	requireAfterInput := false
	if input != "" {
		for i := len(lines) - 1; i >= 0; i-- {
			if end, ok := inputAnchorEndLine(lines, i, input); ok {
				start = end + 1
				requireAfterInput = true
				break
			}
		}
	}
	sawInput := requireAfterInput
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if input != "" && isInputEchoLine(trimmed, input) {
			sawInput = true
			continue
		}
		if _, ok := inputEchoText(trimmed); ok {
			continue
		}
		if shellText, ok := shellInputEchoText(trimmed); ok {
			if input != "" && strings.TrimSpace(shellText) == input {
				sawInput = true
				continue
			}
			if sawInput && strings.TrimSpace(shellText) == "" {
				return true
			}
			continue
		}
		if isTransientStatusLine(trimmed) || isPromptStatusLine(trimmed) || isCodexSuggestionLine(trimmed) {
			continue
		}
		if requireAfterInput && !sawInput {
			continue
		}
		return true
	}
	return input == ""
}

func containsInputEchoLine(text string, input string) bool {
	input = strings.TrimSpace(input)
	if input == "" {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if _, ok := inputAnchorEndLine(lines, i, input); ok {
			return true
		}
	}
	return false
}

func isInputEchoLine(line string, input string) bool {
	text, ok := inputEchoText(line)
	return ok && strings.TrimSpace(text) == strings.TrimSpace(input)
}

func isPromptStatusLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.HasPrefix(lower, "gpt-") && strings.Contains(line, "~")
}

func isCodexSuggestionLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if text, ok := inputEchoText(trimmed); ok {
		trimmed = text
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "implement {feature}"):
		return true
	case strings.HasPrefix(lower, "find and fix a bug in @filename"):
		return true
	case strings.HasPrefix(lower, "improve documentation in @filename"):
		return true
	case strings.HasPrefix(lower, "run /review on my current changes"):
		return true
	case strings.HasPrefix(lower, "use /skills to list available skills"):
		return true
	default:
		return false
	}
}

func compactRepeatedLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	var prev string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed != prev {
			out = append(out, line)
		}
		prev = trimmed
	}
	return strings.Join(out, "\n")
}

func skipEscape(data []byte, i int) int {
	if i+1 >= len(data) {
		return i
	}
	next := data[i+1]
	if next == '[' {
		j := i + 2
		for j < len(data) {
			c := data[j]
			if c >= 0x40 && c <= 0x7e {
				return j
			}
			j++
		}
		return len(data) - 1
	}
	if next == ']' {
		j := i + 2
		for j < len(data) {
			if data[j] == 0x07 {
				return j
			}
			if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
				return j + 1
			}
			j++
		}
		return len(data) - 1
	}
	return i + 1
}
