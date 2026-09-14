-- 239_add_group_skip_codex_default_instructions.sql
-- 分组级开关：OpenAI OAuth 请求缺 instructions 时不再注入 Codex CLI base prompt
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS skip_codex_default_instructions BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.skip_codex_default_instructions IS
    'When true, OpenAI OAuth requests in this group that omit instructions are sent with instructions="" instead of the embedded Codex CLI base prompt';
