import { describe, expect, it } from "vitest";

import {
  normalizeGroupSkipCodexDefaultInstructions,
  supportsGroupSkipCodexDefaultInstructions,
} from "../groupsCodexInstructions";
import en from "@/i18n/locales/en/admin/overview";
import zh from "@/i18n/locales/zh/admin/overview";

describe("groupsCodexInstructions", () => {
  it("supports OpenAI and composite groups", () => {
    expect(supportsGroupSkipCodexDefaultInstructions("openai")).toBe(true);
    expect(supportsGroupSkipCodexDefaultInstructions("composite")).toBe(true);
    expect(supportsGroupSkipCodexDefaultInstructions("anthropic")).toBe(false);
  });

  it("clears stale enabled state on unsupported platforms", () => {
    expect(normalizeGroupSkipCodexDefaultInstructions("openai", true)).toBe(true);
    expect(normalizeGroupSkipCodexDefaultInstructions("composite", true)).toBe(true);
    expect(normalizeGroupSkipCodexDefaultInstructions("anthropic", true)).toBe(false);
    expect(normalizeGroupSkipCodexDefaultInstructions("openai", false)).toBe(false);
  });

  it("provides localized switch labels", () => {
    for (const locale of [zh, en]) {
      expect(locale.groups.codexInstructions).toMatchObject({
        title: expect.any(String),
        skip: expect.any(String),
        hint: expect.stringContaining("instructions"),
      });
    }
  });
});
