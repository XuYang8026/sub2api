// <fork:proxy-smart-import>
package proxyurl

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLine_URLForm_HTTP(t *testing.T) {
	pl, err := ParseLine("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "http" || pl.Host != "proxy.example.com" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
	if pl.NeedsProtocolDetection {
		t.Error("URL form should not need detection")
	}
}

func TestParseLine_URLForm_HTTPS_WithAuth(t *testing.T) {
	pl, err := ParseLine("https://alice:s3cr3t@proxy.example.com:8443")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "https" || pl.Username != "alice" || pl.Password != "s3cr3t" || pl.Port != 8443 {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_URLForm_SOCKS5_UpgradedToSOCKS5H(t *testing.T) {
	pl, err := ParseLine("socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "socks5h" {
		t.Errorf("expected socks5 to auto-upgrade to socks5h, got %q", pl.Protocol)
	}
}

func TestParseLine_URLForm_SOCKS5H_Preserved(t *testing.T) {
	pl, err := ParseLine("socks5h://127.0.0.1:1080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "socks5h" {
		t.Errorf("expected socks5h preserved, got %q", pl.Protocol)
	}
}

func TestParseLine_URLForm_CaseInsensitiveScheme(t *testing.T) {
	pl, err := ParseLine("HTTP://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "http" {
		t.Errorf("expected lower-cased 'http', got %q", pl.Protocol)
	}
}

func TestParseLine_URLForm_UnsupportedScheme(t *testing.T) {
	_, err := ParseLine("ftp://proxy.example.com:21")
	if err == nil || !strings.Contains(err.Error(), "unsupported proxy scheme") {
		t.Fatalf("expected unsupported-scheme error, got %v", err)
	}
}

func TestParseLine_URLForm_MissingPort(t *testing.T) {
	_, err := ParseLine("http://proxy.example.com")
	if err == nil {
		t.Fatal("expected missing-port error")
	}
}

func TestParseLine_URLForm_IPv6(t *testing.T) {
	pl, err := ParseLine("http://[::1]:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "::1" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_BareHostPort(t *testing.T) {
	pl, err := ParseLine("proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Protocol != "" || pl.Host != "proxy.example.com" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
	if !pl.NeedsProtocolDetection {
		t.Error("bare host:port should need protocol detection")
	}
}

func TestParseLine_BareHostPortWithAuth_AtForm(t *testing.T) {
	pl, err := ParseLine("alice:s3cr3t@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 ||
		pl.Username != "alice" || pl.Password != "s3cr3t" {
		t.Errorf("unexpected result: %+v", pl)
	}
	if !pl.NeedsProtocolDetection {
		t.Error("bare auth form should need protocol detection")
	}
}

func TestParseLine_ColonFourField(t *testing.T) {
	pl, err := ParseLine("proxy.example.com:8080:alice:s3cr3t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 ||
		pl.Username != "alice" || pl.Password != "s3cr3t" {
		t.Errorf("unexpected result: %+v", pl)
	}
	if pl.Protocol != "" {
		t.Error("colon 4-field should have empty protocol")
	}
}

func TestParseLine_ColonFourField_PasswordWithColon(t *testing.T) {
	// "host:port:user:pw:with:colons" — password may contain ':'.
	pl, err := ParseLine("h.example.com:8080:alice:p:w:d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Password != "p:w:d" {
		t.Errorf("password should retain embedded colons, got %q", pl.Password)
	}
}

func TestParseLine_PipeForm(t *testing.T) {
	pl, err := ParseLine("proxy.example.com|8080|alice|s3cr3t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 ||
		pl.Username != "alice" || pl.Password != "s3cr3t" {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_PipeForm_TwoField(t *testing.T) {
	pl, err := ParseLine("proxy.example.com|8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
	if pl.Username != "" || pl.Password != "" {
		t.Error("pipe form with 2 fields should not carry credentials")
	}
}

func TestParseLine_CommaForm(t *testing.T) {
	pl, err := ParseLine("proxy.example.com:8080,alice,s3cr3t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 ||
		pl.Username != "alice" || pl.Password != "s3cr3t" {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_CommaForm_EmptyPassword(t *testing.T) {
	pl, err := ParseLine("proxy.example.com:8080,alice,")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Username != "alice" || pl.Password != "" {
		t.Errorf("expected empty password, got %+v", pl)
	}
}

func TestParseLine_URLForm_EmbeddedAtInPassword(t *testing.T) {
	// Explicit percent-encoded '@' in password. url.Parse handles this correctly.
	pl, err := ParseLine("http://alice:p%40ss@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Password != "p@ss" {
		t.Errorf("expected password decoded to 'p@ss', got %q", pl.Password)
	}
}

func TestParseLine_AtForm_PasswordWithAt(t *testing.T) {
	// Passwords with '@' — we split on LAST '@' so this works.
	pl, err := ParseLine("alice:pass@word@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Password != "pass@word" {
		t.Errorf("expected password 'pass@word', got %q", pl.Password)
	}
	if pl.Host != "proxy.example.com" {
		t.Errorf("expected host 'proxy.example.com', got %q", pl.Host)
	}
}

func TestParseLine_AtForm_EmptyPassword(t *testing.T) {
	pl, err := ParseLine("alice:@proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Username != "alice" || pl.Password != "" {
		t.Errorf("expected user with empty password, got %+v", pl)
	}
}

func TestParseLine_EmptyLine(t *testing.T) {
	_, err := ParseLine("")
	if !errors.Is(err, ErrSkipLine) {
		t.Fatalf("expected ErrSkipLine, got %v", err)
	}
}

func TestParseLine_WhitespaceOnly(t *testing.T) {
	_, err := ParseLine("   \t  ")
	if !errors.Is(err, ErrSkipLine) {
		t.Fatalf("expected ErrSkipLine, got %v", err)
	}
}

func TestParseLine_CommentLine(t *testing.T) {
	_, err := ParseLine("# this is a comment")
	if !errors.Is(err, ErrSkipLine) {
		t.Fatalf("expected ErrSkipLine, got %v", err)
	}
}

func TestParseLine_LeadingBOM(t *testing.T) {
	pl, err := ParseLine(utf8BOM + "proxy.example.com:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_PortOutOfRange(t *testing.T) {
	_, err := ParseLine("proxy.example.com:99999")
	if err == nil {
		t.Fatal("expected port-out-of-range error")
	}
}

func TestParseLine_PortZero(t *testing.T) {
	_, err := ParseLine("proxy.example.com:0")
	if err == nil {
		t.Fatal("expected port-out-of-range error (port 0)")
	}
}

func TestParseLine_PortNonNumeric(t *testing.T) {
	_, err := ParseLine("proxy.example.com:abc")
	if err == nil {
		t.Fatal("expected non-numeric-port error")
	}
}

func TestParseLine_HostEmpty(t *testing.T) {
	_, err := ParseLine(":8080")
	if err == nil {
		t.Fatal("expected empty-host error")
	}
}

func TestParseLine_IPv6_UnbracketedIsRejected(t *testing.T) {
	// Multi-colon non-URL, non-bracket lines are ambiguous — reject.
	_, err := ParseLine("::1:8080")
	if err == nil {
		t.Fatal("unbracketed IPv6 with extra colon should not be a valid 4-field colon form here")
	}
}

func TestParseLine_IPv6_BracketedBareForm(t *testing.T) {
	pl, err := ParseLine("[::1]:8080")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "::1" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_IPv6_BracketedFourField(t *testing.T) {
	pl, err := ParseLine("[::1]:8080:alice:s3cr3t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "::1" || pl.Port != 8080 || pl.Username != "alice" || pl.Password != "s3cr3t" {
		t.Errorf("unexpected result: %+v", pl)
	}
}

func TestParseLine_WhitespaceInMiddleOfHost(t *testing.T) {
	_, err := ParseLine("proxy example.com:8080")
	if err == nil {
		t.Fatal("expected error for whitespace in host")
	}
}

func TestParseLine_Malformed_NoDelimiters(t *testing.T) {
	_, err := ParseLine("randomrubbish")
	if err == nil {
		t.Fatal("expected error for input with no delimiters")
	}
}

func TestParseLine_Malformed_URL_MissingHost(t *testing.T) {
	_, err := ParseLine("http://:8080")
	if err == nil {
		t.Fatal("expected error for URL missing host")
	}
}

func TestParseLine_TrimsSurroundingWhitespace(t *testing.T) {
	pl, err := ParseLine("   proxy.example.com:8080   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pl.Host != "proxy.example.com" || pl.Port != 8080 {
		t.Errorf("unexpected result: %+v", pl)
	}
}

// --- ParseLines batch tests --------------------------------------------------

func TestParseLines_MixedFormats(t *testing.T) {
	input := strings.Join([]string{
		"http://proxy1.example.com:8080",
		"proxy2.example.com:8081",
		"alice:pw@proxy3.example.com:8082",
		"proxy4.example.com:8083:bob:pw2",
		"proxy5.example.com|8084|carol|pw3",
		"proxy6.example.com:8085,dave,pw4",
	}, "\n")
	results, errs := ParseLines(input)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}
}

func TestParseLines_SkipEmptyAndComments(t *testing.T) {
	input := "\n\n# comment\nproxy.example.com:8080\n   \n"
	results, errs := ParseLines(input)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestParseLines_PartialErrors(t *testing.T) {
	input := "proxy1.example.com:8080\nBADFORMAT\nproxy2.example.com:8081"
	results, errs := ParseLines(input)
	if len(results) != 2 {
		t.Errorf("expected 2 valid, got %d", len(results))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].LineNumber != 2 {
		t.Errorf("expected error on line 2, got %d", errs[0].LineNumber)
	}
	if !strings.Contains(errs[0].Error(), "line 2") {
		t.Errorf("LineError should include line number in string: %s", errs[0].Error())
	}
}

func TestParseLines_MixedLineEndings_CRLF(t *testing.T) {
	input := "proxy1.example.com:8080\r\nproxy2.example.com:8081\r\n"
	results, errs := ParseLines(input)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestParseLines_MixedLineEndings_CR(t *testing.T) {
	input := "proxy1.example.com:8080\rproxy2.example.com:8081"
	results, errs := ParseLines(input)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestParseLines_Empty(t *testing.T) {
	results, errs := ParseLines("")
	if len(errs) != 0 || len(results) != 0 {
		t.Errorf("expected empty result, got %d results / %d errors", len(results), len(errs))
	}
}

// <fork:proxy-smart-import> host-validation hardening — reject control chars
// and URL structural separators so a malformed line cannot smuggle an extra
// authority/path segment through the bare-form parsers.
func TestParseLine_HostRejects_NullByte(t *testing.T) {
	_, err := ParseLine("host\x00attacker.com:8080")
	if err == nil {
		t.Fatal("expected error for NUL byte in host")
	}
}

func TestParseLine_HostRejects_ControlChar(t *testing.T) {
	_, err := ParseLine("host\x01evil.com:8080")
	if err == nil {
		t.Fatal("expected error for control byte in host")
	}
}

func TestParseLine_HostRejects_DEL(t *testing.T) {
	_, err := ParseLine("host\x7fevil.com:8080")
	if err == nil {
		t.Fatal("expected error for DEL byte in host")
	}
}

func TestParseLine_HostRejects_Slash(t *testing.T) {
	_, err := ParseLine("host/path:8080")
	if err == nil {
		t.Fatal("expected error for '/' in host")
	}
}

func TestParseLine_HostRejects_Backslash(t *testing.T) {
	_, err := ParseLine("host\\path:8080")
	if err == nil {
		t.Fatal("expected error for '\\' in host")
	}
}

func TestParseLine_HostRejects_Question(t *testing.T) {
	_, err := ParseLine("host?q=1:8080")
	if err == nil {
		t.Fatal("expected error for '?' in host")
	}
}

func TestParseLine_HostRejects_Hash(t *testing.T) {
	_, err := ParseLine("host#tag:8080")
	if err == nil {
		t.Fatal("expected error for '#' in host")
	}
}

func TestParseLine_URLForm_HostRejects_Slash(t *testing.T) {
	// url.Parse would gobble "host/path" as host="host", path="/path" — but
	// our validateHost still runs after url.Hostname() strips the path, so
	// we specifically want to catch cases where the host itself contains
	// a slash after unbracketing (rare, but exercise the validator).
	_, err := ParseLine("http://host\\evil.com:8080")
	if err == nil {
		t.Fatal("expected error for '\\' in URL host")
	}
}
