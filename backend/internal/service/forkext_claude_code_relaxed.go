package service

import "net/http"

// <fork:relax-claude-code-detect>
//
// Sidecar policy that relaxes Claude Code detection to UA-only. Registered
// via init() so the upstream claude_code_validator.go stays free of any
// fork-specific policy knowledge.
//
// Rationale: the original strict pipeline (system prompt Dice ≥ 0.5 + X-App
// / anthropic-beta / anthropic-version headers + metadata.user_id format)
// rejects real Claude Code traffic in these production scenarios:
//   - Custom sub-agents (.claude/agents/*.md) with author-supplied system
//     prompts whose Dice similarity to the 6 official templates is too low.
//   - Task-tool-derived agents (general-purpose / code-reviewer / …).
//   - Reverse proxies (nginx / CF) that strip non-whitelisted headers.
//   - Agent SDK internal sub-requests without a full metadata block.
//
// Tradeoff: UA is trivially forgeable (curl -H does it), but this is a
// private deployment behind API-key auth — the client-type check is a soft
// router, not a security boundary. Real Claude Code traffic passing again
// outweighs the risk of a UA-spoofed non-CC client claiming CC privilege.
//
// Deleting this file re-enables the strict policy without further changes.

func init() {
	SetClaudeCodeDeepValidationPolicy(relaxedClaudeCodeDeepValidation)
}

func relaxedClaudeCodeDeepValidation(_ *ClaudeCodeValidator, _ *http.Request, _ map[string]any) bool {
	return true
}

// </fork:relax-claude-code-detect>
