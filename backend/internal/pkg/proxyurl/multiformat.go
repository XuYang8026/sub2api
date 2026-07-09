// <fork:proxy-smart-import>
// multiformat.go — parses various proxy line formats into a common structured
// representation. Consumed by the admin "smart batch import" endpoint so the
// UI can accept messy free-form input (one proxy per line) without forcing the
// user to normalize each entry by hand.
//
// Supported formats (whitespace and BOM trimmed per line, comments starting
// with '#' skipped, empty lines skipped):
//
//	1. protocol://[user:pass@]host:port      — canonical URL form
//	2. host:port                             — bare, no protocol (needs detection)
//	3. user:pass@host:port                   — bare with auth (needs detection)
//	4. host:port:user:pass                   — colon-separated 4-field
//	5. host|port|user|pass                   — pipe-separated
//	6. host:port,user,pass                   — mixed comma variant
//
// Any format that does not carry an explicit scheme sets
// ParsedLine.NeedsProtocolDetection = true so the caller can probe.
package proxyurl

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParsedLine is the structured result of parsing one input line.
type ParsedLine struct {
	// Protocol is the lower-cased scheme. Empty string means "unknown, caller
	// should probe". When present it is one of: http, https, socks5, socks5h.
	// Note: socks5 is normalized to socks5h to match parse.go behavior and
	// avoid client-side DNS resolution.
	Protocol string
	Host     string
	Port     int
	Username string
	Password string
	// RawInput is the trimmed original line (safe for logs — but callers
	// should still be careful, credentials may be present).
	RawInput string
	// NeedsProtocolDetection is true iff Protocol == "" after parsing.
	NeedsProtocolDetection bool
}

// LineError describes a per-line parse failure produced by ParseLines.
type LineError struct {
	LineNumber int    // 1-indexed
	RawInput   string // trimmed line (may contain credentials — caller decides how to surface)
	Err        error
}

func (e LineError) Error() string {
	return fmt.Sprintf("line %d: %v", e.LineNumber, e.Err)
}

func (e LineError) Unwrap() error { return e.Err }

// allowedSchemesMulti mirrors parse.go's allowedSchemes. Duplicated (not
// referenced) to keep this file's public contract self-contained and to avoid
// accidental cross-package coupling if parse.go's set ever narrows.
var allowedSchemesMulti = map[string]bool{
	"http":    true,
	"https":   true,
	"socks5":  true,
	"socks5h": true,
}

// utf8BOM is the byte-order-mark that browsers sometimes prepend to
// clipboard/upload payloads (U+FEFF encoded as EF BB BF).
const utf8BOM = "\ufeff"

// ParseLine parses one proxy line. It returns an error for empty/comment
// lines only in the sense that the caller should skip — an empty raw input
// yields (nil, errSkip). ParseLines takes care of that dispatch; direct
// callers should treat ErrSkipLine as "no-op, ignore".
func ParseLine(raw string) (*ParsedLine, error) {
	line := strings.TrimPrefix(raw, utf8BOM)
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, ErrSkipLine
	}
	if strings.HasPrefix(line, "#") {
		return nil, ErrSkipLine
	}

	// Detection order — URL form first (anything with "scheme://"). We check
	// case-insensitively. Unsupported schemes are surfaced as a descriptive
	// error rather than falling through to bare parsing (which would produce
	// a confusing "invalid port" message).
	if scheme, ok := detectURLScheme(line); ok {
		if !allowedSchemesMulti[scheme] {
			return nil, fmt.Errorf("unsupported proxy scheme %q (allowed: http, https, socks5, socks5h)", scheme)
		}
		return parseURLForm(line)
	}

	// Non-URL bare forms. Delimiter-based dispatch:
	//   - contains '|' → pipe-separated (host|port|user|pass)
	//   - contains ',' → comma variant (host:port,user,pass)
	//   - otherwise → colon-separated (may include '@' for bare auth form)
	if strings.Contains(line, "|") {
		return parsePipeForm(line)
	}
	if strings.Contains(line, ",") {
		return parseCommaForm(line)
	}
	// Distinguish "user:pass@host:port" (has '@') from "host:port:user:pass".
	if strings.Contains(line, "@") {
		return parseAtForm(line)
	}
	return parseColonForm(line)
}

// ErrSkipLine is returned by ParseLine for empty/comment lines.
var ErrSkipLine = errors.New("skip line")

// ParseLines parses a whole batch — one line per proxy. Returns the parsed
// entries and a slice of per-line errors. Empty and comment lines are silently
// skipped and do not appear in either slice.
func ParseLines(input string) (results []*ParsedLine, errs []LineError) {
	// Normalize line endings — support LF, CRLF, and lone CR (older mac clipboards).
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	for i, raw := range strings.Split(normalized, "\n") {
		parsed, err := ParseLine(raw)
		if err != nil {
			if errors.Is(err, ErrSkipLine) {
				continue
			}
			errs = append(errs, LineError{
				LineNumber: i + 1,
				RawInput:   strings.TrimSpace(strings.TrimPrefix(raw, utf8BOM)),
				Err:        err,
			})
			continue
		}
		results = append(results, parsed)
	}
	return results, errs
}

// --- URL form -----------------------------------------------------------------

// detectURLScheme returns the lower-cased scheme portion if line starts with
// "<scheme>://" where scheme matches [a-zA-Z][a-zA-Z0-9+.-]*. Returns ok=false
// otherwise. This lets us distinguish "unknown scheme URLs" from "bare
// host:port"-style input.
func detectURLScheme(line string) (string, bool) {
	idx := strings.Index(line, "://")
	if idx <= 0 {
		return "", false
	}
	scheme := line[:idx]
	if scheme == "" {
		return "", false
	}
	// Scheme must be alphanumeric plus +/-/. per RFC 3986.
	for i, r := range scheme {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return "", false
			}
			continue
		}
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '+' || r == '.' || r == '-') {
			return "", false
		}
	}
	return strings.ToLower(scheme), true
}

func parseURLForm(line string) (*ParsedLine, error) {
	parsed, err := url.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %v", err)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("proxy URL missing host")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if !allowedSchemesMulti[scheme] {
		return nil, fmt.Errorf("unsupported proxy scheme %q (allowed: http, https, socks5, socks5h)", scheme)
	}
	// Normalize socks5 → socks5h to match parse.go behavior.
	if scheme == "socks5" {
		scheme = "socks5h"
	}
	host := parsed.Hostname()
	portStr := parsed.Port()
	if portStr == "" {
		return nil, fmt.Errorf("proxy URL missing port")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return nil, err
	}
	if err := validateHost(host); err != nil {
		return nil, err
	}
	pl := &ParsedLine{
		Protocol: scheme,
		Host:     host,
		Port:     port,
		RawInput: line,
	}
	if parsed.User != nil {
		pl.Username = parsed.User.Username()
		if pw, ok := parsed.User.Password(); ok {
			pl.Password = pw
		}
	}
	return pl, nil
}

// --- host:port bare / host:port:user:pass / user:pass@host:port ---------------

// parseColonForm handles both "host:port" and "host:port:user:pass". If an
// IPv6 host in brackets is present, split on ']' first.
func parseColonForm(line string) (*ParsedLine, error) {
	host, rest, err := splitHostPortRest(line)
	if err != nil {
		return nil, err
	}
	// rest is the remainder after "host:port", which is either "" or
	// ":user:pass" (still colon-prefixed) — we already consumed host and port.
	fields := []string{host.hostStr, strconv.Itoa(host.port)}
	if rest != "" {
		rest = strings.TrimPrefix(rest, ":")
		// user:pass — split on the FIRST ':' so passwords containing ':' work.
		// But because '@' is not in this path (parseAtForm handles that) we
		// don't need to worry about auth-delimiter ambiguity here.
		if rest != "" {
			parts := strings.SplitN(rest, ":", 2)
			fields = append(fields, parts...)
		}
	}
	switch len(fields) {
	case 2:
		return &ParsedLine{
			Host:                   host.hostStr,
			Port:                   host.port,
			RawInput:               line,
			NeedsProtocolDetection: true,
		}, nil
	case 3:
		return &ParsedLine{
			Host:                   host.hostStr,
			Port:                   host.port,
			Username:               fields[2],
			RawInput:               line,
			NeedsProtocolDetection: true,
		}, nil
	case 4:
		return &ParsedLine{
			Host:                   host.hostStr,
			Port:                   host.port,
			Username:               fields[2],
			Password:               fields[3],
			RawInput:               line,
			NeedsProtocolDetection: true,
		}, nil
	default:
		return nil, fmt.Errorf("unrecognized proxy format")
	}
}

// parseAtForm handles "user:pass@host:port". Splits on the LAST '@' so
// passwords may contain '@'.
func parseAtForm(line string) (*ParsedLine, error) {
	idx := strings.LastIndex(line, "@")
	if idx < 0 {
		return nil, fmt.Errorf("expected '@' in bare-auth proxy format")
	}
	authPart := line[:idx]
	hostPart := line[idx+1:]
	host, err := parseHostPort(hostPart)
	if err != nil {
		return nil, err
	}
	// authPart is "user:pass" — split on FIRST ':' so passwords may contain ':'.
	authFields := strings.SplitN(authPart, ":", 2)
	if len(authFields) != 2 {
		return nil, fmt.Errorf("expected 'user:pass' before '@'")
	}
	if authFields[0] == "" {
		return nil, fmt.Errorf("username may not be empty")
	}
	return &ParsedLine{
		Host:                   host.hostStr,
		Port:                   host.port,
		Username:               authFields[0],
		Password:               authFields[1],
		RawInput:               line,
		NeedsProtocolDetection: true,
	}, nil
}

// --- pipe / comma variants ---------------------------------------------------

func parsePipeForm(line string) (*ParsedLine, error) {
	parts := strings.Split(line, "|")
	if len(parts) < 2 || len(parts) > 4 {
		return nil, fmt.Errorf("pipe-separated format expects 2-4 fields, got %d", len(parts))
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	host := parts[0]
	if err := validateHost(host); err != nil {
		return nil, err
	}
	port, err := parsePort(parts[1])
	if err != nil {
		return nil, err
	}
	pl := &ParsedLine{
		Host:                   host,
		Port:                   port,
		RawInput:               line,
		NeedsProtocolDetection: true,
	}
	if len(parts) >= 3 {
		pl.Username = parts[2]
	}
	if len(parts) == 4 {
		pl.Password = parts[3]
	}
	return pl, nil
}

// parseCommaForm handles "host:port,user,pass".
func parseCommaForm(line string) (*ParsedLine, error) {
	parts := strings.Split(line, ",")
	if len(parts) < 1 || len(parts) > 3 {
		return nil, fmt.Errorf("comma-separated format expects 1-3 fields, got %d", len(parts))
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	host, err := parseHostPort(parts[0])
	if err != nil {
		return nil, err
	}
	pl := &ParsedLine{
		Host:                   host.hostStr,
		Port:                   host.port,
		RawInput:               line,
		NeedsProtocolDetection: true,
	}
	if len(parts) >= 2 {
		pl.Username = parts[1]
	}
	if len(parts) == 3 {
		pl.Password = parts[2]
	}
	return pl, nil
}

// --- shared helpers ----------------------------------------------------------

// hostPort is a parsed host:port pair — host may be a bare hostname/IPv4 or
// an unbracketed IPv6 literal (stripped from "[::1]:8080" form).
type hostPort struct {
	hostStr string
	port    int
}

// parseHostPort parses "host:port" or "[ipv6]:port". Returns an error if the
// port is invalid or the host fails validateHost.
func parseHostPort(s string) (hostPort, error) {
	s = strings.TrimSpace(s)
	// Bracketed IPv6 form
	if strings.HasPrefix(s, "[") {
		host, portStr, err := net.SplitHostPort(s)
		if err != nil {
			return hostPort{}, fmt.Errorf("invalid IPv6 host:port: %v", err)
		}
		port, err := parsePort(portStr)
		if err != nil {
			return hostPort{}, err
		}
		if err := validateHost(host); err != nil {
			return hostPort{}, err
		}
		return hostPort{hostStr: host, port: port}, nil
	}
	// Plain "host:port" — MUST have exactly one ':' otherwise it is a
	// four-field colon form (handled by splitHostPortRest, not here).
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return hostPort{}, fmt.Errorf("missing ':port' in %q", s)
	}
	host := s[:idx]
	portStr := s[idx+1:]
	if strings.Contains(host, ":") {
		return hostPort{}, fmt.Errorf("unexpected ':' in host %q — use [ipv6]:port for IPv6", host)
	}
	if err := validateHost(host); err != nil {
		return hostPort{}, err
	}
	port, err := parsePort(portStr)
	if err != nil {
		return hostPort{}, err
	}
	return hostPort{hostStr: host, port: port}, nil
}

// splitHostPortRest peels off a leading "host:port" segment and returns the
// remainder, which may be empty or start with ":..." for extra fields. Handles
// bracketed IPv6.
func splitHostPortRest(s string) (hostPort, string, error) {
	s = strings.TrimSpace(s)
	// Bracketed IPv6: [::1]:port[:rest]
	if strings.HasPrefix(s, "]") {
		return hostPort{}, "", fmt.Errorf("unexpected ']' at start of %q", s)
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return hostPort{}, "", fmt.Errorf("unmatched '[' in %q", s)
		}
		// After "]" we expect ":port" possibly followed by more.
		hostLit := s[1:end]
		afterBracket := s[end+1:]
		if !strings.HasPrefix(afterBracket, ":") {
			return hostPort{}, "", fmt.Errorf("expected ':port' after ']' in %q", s)
		}
		afterBracket = afterBracket[1:] // strip leading ':'
		// portField ends at next ':' (start of rest) or end of string.
		portEnd := strings.Index(afterBracket, ":")
		var portStr, rest string
		if portEnd < 0 {
			portStr = afterBracket
			rest = ""
		} else {
			portStr = afterBracket[:portEnd]
			rest = afterBracket[portEnd:] // includes leading ':'
		}
		port, err := parsePort(portStr)
		if err != nil {
			return hostPort{}, "", err
		}
		if err := validateHost(hostLit); err != nil {
			return hostPort{}, "", err
		}
		return hostPort{hostStr: hostLit, port: port}, rest, nil
	}
	// Non-bracketed: split on first ':' for host, then again on the port boundary.
	firstColon := strings.Index(s, ":")
	if firstColon < 0 {
		return hostPort{}, "", fmt.Errorf("missing ':port' in %q", s)
	}
	host := s[:firstColon]
	afterHost := s[firstColon+1:]
	// port ends at next ':' or end of string
	nextColon := strings.Index(afterHost, ":")
	var portStr, rest string
	if nextColon < 0 {
		portStr = afterHost
		rest = ""
	} else {
		portStr = afterHost[:nextColon]
		rest = afterHost[nextColon:]
	}
	if err := validateHost(host); err != nil {
		return hostPort{}, "", err
	}
	port, err := parsePort(portStr)
	if err != nil {
		return hostPort{}, "", err
	}
	return hostPort{hostStr: host, port: port}, rest, nil
}

func parsePort(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("port is empty")
	}
	p, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("port out of range: %d", p)
	}
	return p, nil
}

func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("host is empty")
	}
	if strings.ContainsAny(host, " \t\r\n") {
		return fmt.Errorf("host contains whitespace: %q", host)
	}
	// <fork:proxy-smart-import> reject control characters and URL structural
	// separators to prevent malformed lines from smuggling authority/path
	// segments (e.g. "host\x00attacker.com" or "host/path") through the
	// bare-form parsers, which would otherwise happily accept them.
	// Note: bracketed IPv6 hosts have their brackets stripped before reaching
	// here, so ':' is not part of the byte set we need to reject.
	for i := 0; i < len(host); i++ {
		b := host[i]
		if b < 0x20 || b == 0x7F {
			return fmt.Errorf("host contains control character: %q", host)
		}
		switch b {
		case '/', '\\', '?', '#', '@':
			return fmt.Errorf("host contains disallowed character %q: %q", b, host)
		}
	}
	return nil
}
