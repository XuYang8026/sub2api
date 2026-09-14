export function supportsGroupSkipCodexDefaultInstructions(
  platform: string,
): boolean {
  return platform === "openai" || platform === "composite";
}

export function normalizeGroupSkipCodexDefaultInstructions(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupSkipCodexDefaultInstructions(platform) && enabled;
}
