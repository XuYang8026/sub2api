package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// <fork:codex-default-instructions>
//
// 分组级开关：OpenAI OAuth（Codex 协议）请求缺 instructions 时，上游默认会填入内嵌的
// Codex CLI base prompt（约 21 KB ≈ 4k token），下游按 input 计费并产生缓存命中。
// 开启 Group.SkipCodexDefaultInstructions 后，三处补齐点全部跳过，改为发 instructions=""
// （与 /v1/chat/completions 路径一致，生产已验证 Codex 后端接受）。
//
// 分组来源与 openAIGroupForcesFast 同源：ctx.Value(ctxkey.Group)。
// 仅 openai / composite 平台分组生效，与 groupSupportsOpenAIFast 对齐；
// 非该两类平台的分组在保存时由 sanitizeGroupOpenAIFast 清洗为 false。
//
// 调用点（均带同名标记）：
//   - openai_gateway_forward.go：/v1/responses 热路径补齐 + 非桥接 Codex 转换
//   - openai_gateway_chat_completions.go：/v1/chat/completions 入站的 Responses 形态分支
//   - openai_gateway_passthrough.go：passthrough 模式对 *codex* 模型的补齐
func groupSkipsCodexDefaultInstructions(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	return IsGroupContextValid(group) && groupSupportsOpenAIFast(group.Platform) && group.SkipCodexDefaultInstructions
}

// </fork:codex-default-instructions>
