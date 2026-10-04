package term

import (
	"regexp"
	"strings"
	"time"

	"github.com/lmorchard/wideboi/internal/protocol"
)

// heuristicDebounceWindow is the minimum silence period after PTY output
// before scanning screen contents and title for interactive agent prompts.
const heuristicDebounceWindow = 500 * time.Millisecond

// DefaultWorkingInactivityTimeout is the maximum duration a pane may report
// StatusWorking without any PTY output before being flagged as StatusInterrupted.
const DefaultWorkingInactivityTimeout = 30 * time.Second

var (
	// regexProgress matches common ASCII / Unicode progress bar blocks.
	regexProgress = regexp.MustCompile(`(■|⬝){4,}|\[={5,}>`)

	// regexGenericConfirm matches interactive command-line confirmation prompts like [y/N], (y/n), etc.
	regexGenericConfirm = regexp.MustCompile(`(?i)(\[[yY]/[nN](\?)?\]|\([yY]/[nN]\)|\[yes/no\]|\(yes/no\))`)
)

// isWorkingTitle checks if the terminal title contains active spinner glyphs
// (e.g. Braille spinners or arc quadrants commonly emitted by Claude Code and Codex).
func isWorkingTitle(title string) bool {
	if title == "" {
		return false
	}
	for _, r := range title {
		if (r >= 0x2800 && r <= 0x28FF) || (r >= 0x25D0 && r <= 0x25D3) {
			return true
		}
	}
	return false
}

// isBlockerTitle checks if the terminal title explicitly signals that human attention is required.
func isBlockerTitle(title string) bool {
	if title == "" {
		return false
	}
	return strings.Contains(strings.ToLower(title), "action required")
}

// scanScreenTail inspects the non-empty lines from the bottom of the visible screen
// for agent blocker UI signatures or active working indicators.
// It returns (status, matched). If matched is false, the caller should fall back to its
// standard idle timeout logic.
func scanScreenTail(lines []string) (protocol.PaneStatus, bool) {
	if len(lines) == 0 {
		return protocol.StatusIdle, false
	}

	// Filter down to non-empty trimmed lines, keeping up to the last 12.
	var nonBlank []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			nonBlank = append(nonBlank, trimmed)
		}
	}
	if len(nonBlank) == 0 {
		return protocol.StatusIdle, false
	}
	if len(nonBlank) > 12 {
		nonBlank = nonBlank[len(nonBlank)-12:]
	}

	// 1. Check for active working / in-progress indicators across recent output.
	combined := strings.ToLower(strings.Join(nonBlank, "\n"))
	if strings.Contains(combined, "esc to interrupt") ||
		strings.Contains(combined, "ctrl+c to interrupt") ||
		strings.Contains(combined, "press esc to interrupt") {
		return protocol.StatusWorking, true
	}
	if regexProgress.MatchString(combined) {
		return protocol.StatusWorking, true
	}

	// 2. Check for active blocker / confirmation prompts.
	// An active blocker must be at the very bottom of the terminal output.
	// If the user has already responded and subsequent command output was written
	// beneath the prompt, the blocker is historical and no longer active.
	trailing := nonBlank
	if len(trailing) > 3 {
		trailing = trailing[len(trailing)-3:]
	}
	trailingCombined := strings.ToLower(strings.Join(trailing, "\n"))

	// OpenCode permission prompt:
	if strings.Contains(trailingCombined, "permission required") {
		return protocol.StatusNeedsInput, true
	}
	if strings.Contains(trailingCombined, "esc dismiss") &&
		(strings.Contains(trailingCombined, "enter confirm") ||
			strings.Contains(trailingCombined, "enter submit") ||
			strings.Contains(trailingCombined, "enter toggle")) {
		return protocol.StatusNeedsInput, true
	}

	// Claude Code permission / confirmation prompts:
	if strings.Contains(trailingCombined, "do you want to proceed") ||
		strings.Contains(trailingCombined, "waiting for permission") ||
		strings.Contains(trailingCombined, "do you want to run") ||
		strings.Contains(trailingCombined, "do you want to allow") {
		return protocol.StatusNeedsInput, true
	}
	if strings.Contains(trailingCombined, "esc to cancel") &&
		(strings.Contains(trailingCombined, "enter to confirm") ||
			strings.Contains(trailingCombined, "enter to select") ||
			strings.Contains(trailingCombined, "navigate") ||
			strings.Contains(trailingCombined, "arrow")) {
		return protocol.StatusNeedsInput, true
	}

	// Codex approval / directory trust:
	if strings.Contains(trailingCombined, "trust the contents of this directory") ||
		strings.Contains(trailingCombined, "trust and continue") {
		return protocol.StatusNeedsInput, true
	}

	// Generic interactive prompt (e.g. [y/N], (y/n)) on the very last non-blank line:
	lastLine := nonBlank[len(nonBlank)-1]
	if regexGenericConfirm.MatchString(lastLine) {
		return protocol.StatusNeedsInput, true
	}

	return protocol.StatusIdle, false
}
