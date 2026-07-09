// <fork:proxy-circuit-breaker>
package repository

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateProbeError_ASCIIUnderLimit asserts short strings pass through
// unchanged (no trailing "..." appended).
func TestTruncateProbeError_ASCIIUnderLimit(t *testing.T) {
	in := "short error"
	got := truncateProbeError(in, maxProxyProbeErrorLen)
	if got != in {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

// TestTruncateProbeError_ASCIIOverLimit asserts truncation of pure ASCII
// produces a bounded string with the ellipsis suffix.
func TestTruncateProbeError_ASCIIOverLimit(t *testing.T) {
	in := strings.Repeat("a", maxProxyProbeErrorLen+50)
	got := truncateProbeError(in, maxProxyProbeErrorLen)
	if len(got) != maxProxyProbeErrorLen+3 { // "..." suffix
		t.Fatalf("expected len %d, got %d", maxProxyProbeErrorLen+3, len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got[len(got)-5:])
	}
}

// TestTruncateProbeError_MultibyteBoundary asserts that a long Chinese string
// truncated at a byte boundary that would fall mid-rune still yields a valid
// UTF-8 result.
func TestTruncateProbeError_MultibyteBoundary(t *testing.T) {
	// 中 is 3 bytes in UTF-8. Choose maxBytes = 4 so a naive truncate would
	// cut the second 中 in half.
	in := "abc中文测试" // 3 + 3 + 3 + 3 + 3 = 15 bytes
	got := truncateProbeError(in, 4)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	// Guarantees no partial rune leaked before the ellipsis.
	trimmed := strings.TrimSuffix(got, "...")
	if !utf8.ValidString(trimmed) {
		t.Fatalf("trimmed prefix is not valid UTF-8: %q", trimmed)
	}
}

// TestTruncateProbeError_LongChinese exercises the real production limit
// with a string exclusively composed of multibyte runes.
func TestTruncateProbeError_LongChinese(t *testing.T) {
	// 200 copies of "代理错误" (each rune 3 bytes, 4 runes = 12 bytes)
	// yields 2400 bytes, well over maxProxyProbeErrorLen (500).
	in := strings.Repeat("代理错误", 200)
	got := truncateProbeError(in, maxProxyProbeErrorLen)
	if !utf8.ValidString(got) {
		t.Fatalf("output is not valid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix")
	}
	// Prefix must not exceed maxProxyProbeErrorLen bytes.
	trimmed := strings.TrimSuffix(got, "...")
	if len(trimmed) > maxProxyProbeErrorLen {
		t.Fatalf("prefix len %d exceeds limit %d", len(trimmed), maxProxyProbeErrorLen)
	}
}

// </fork>
