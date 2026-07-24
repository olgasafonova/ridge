package scanner

import (
	"regexp"
	"strings"
	"unicode"
)

// redactedPlaceholder replaces secret values in sample snippets. Snippets flow
// to MCP callers via Node.Source (arch_scan detail="full" samples=true), so a
// hardcoded key in scanned source would otherwise be echoed verbatim. This is
// the HG-2 sanitization idiom applied to success payloads rather than error
// strings.
const redactedPlaceholder = "[REDACTED]"

// secretKeyAssignRe matches assignments and JSON/YAML entries whose key names
// a credential (api_key, secret, token, password, ...). Group 1 keeps the key
// and separator; group 2 is the value to redact.
var secretKeyAssignRe = regexp.MustCompile(
	`(?i)([\w.-]*(?:api[_-]?key|apikey|secret|token|password|passwd|credential|auth)[\w-]*["']?\s*[:=]+\s*)` +
		`("(?:[^"\\]|\\.)*"|'[^'\\]*'|` + "`[^`]*`" + `|[^"'` + "`" + `\s,;)}\]]+)`)

// bearerTokenRe matches "Bearer <token>" credentials; group 1 keeps the scheme.
var bearerTokenRe = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{16,}`)

// tokenPrefixRes match well-known credential shapes as bare strings, without
// needing an assignment context.
var tokenPrefixRes = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`),        // OpenAI/Anthropic-style
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{16,}`),         // GitHub personal access token
	regexp.MustCompile(`\bgho_[A-Za-z0-9]{16,}`),         // GitHub OAuth token
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), // GitHub fine-grained PAT
	regexp.MustCompile(`\bxox[bp]-[A-Za-z0-9-]{10,}`),    // Slack bot/user token
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),           // AWS access key ID
}

// quotedAssignValueRe matches a quoted string in an assignment context whose
// body stays within the base64/hex alphabet for 32+ characters. Entropy is
// checked separately in redactHighEntropyValue to keep this conservative.
var quotedAssignValueRe = regexp.MustCompile("([:=]\\s*)([\"'`])([A-Za-z0-9+/=_-]{32,})([\"'`])")

// safeAssignValues are unquoted values that never carry a secret; masking them
// would only obscure ordinary code.
var safeAssignValues = map[string]bool{
	"nil": true, "null": true, "none": true, "true": true, "false": true,
	// "Bearer" as a keyed value means the real token follows; bearerTokenRe
	// already handled it by the time secretKeyAssignRe runs.
	"bearer": true, "basic": true,
	strings.ToLower(redactedPlaceholder): true,
}

// maskSecrets redacts credential-shaped content from a single sample line:
// keyed assignments (api_key = "..."), bearer tokens, well-known token
// prefixes (ghp_, sk-, AKIA...), and long high-entropy quoted literals in
// assignment position. Ordinary code passes through unchanged.
func maskSecrets(line string) string {
	line = bearerTokenRe.ReplaceAllString(line, "${1}"+redactedPlaceholder)
	line = secretKeyAssignRe.ReplaceAllStringFunc(line, redactKeyedValue)
	for _, re := range tokenPrefixRes {
		line = re.ReplaceAllString(line, redactedPlaceholder)
	}
	line = quotedAssignValueRe.ReplaceAllStringFunc(line, redactHighEntropyValue)
	return line
}

// maskSnippet applies maskSecrets to every line of a multi-line snippet.
func maskSnippet(snippet string) string {
	lines := strings.Split(snippet, "\n")
	for i, l := range lines {
		lines[i] = maskSecrets(l)
	}
	return strings.Join(lines, "\n")
}

// redactKeyedValue rewrites one secretKeyAssignRe match, keeping the key and
// separator and replacing the value. Quoted values keep their quote style.
// Unquoted values that look like expressions (function calls) or safe
// literals are left alone so ordinary code isn't mangled.
func redactKeyedValue(match string) string {
	m := secretKeyAssignRe.FindStringSubmatch(match)
	key, val := m[1], m[2]
	lkey := strings.ToLower(key)
	// "author", "authored_by" etc. contain "auth" but never a credential;
	// "authorization" (via "authoriz") stays masked.
	if strings.Contains(lkey, "author") && !strings.Contains(lkey, "authoriz") {
		return match
	}
	if q, quoted := openingQuote(val); quoted {
		return key + q + redactedPlaceholder + q
	}
	if strings.Contains(val, "(") || safeAssignValues[strings.ToLower(val)] {
		return match
	}
	return key + redactedPlaceholder
}

// openingQuote returns the quote character opening val when val is a quoted
// string of at least two characters, or ok=false otherwise.
func openingQuote(val string) (q string, ok bool) {
	if len(val) < 2 {
		return "", false
	}
	switch val[0] {
	case '"', '\'', '`':
		return string(val[0]), true
	}
	return "", false
}

// redactHighEntropyValue rewrites one quotedAssignValueRe match when the
// quoted body actually looks like key material: matching quote pair plus at
// least one letter and one digit. Long all-alpha words and URL-shaped strings
// (excluded upstream by the alphabet, which has no ':' '.' or '/'-with-dots)
// pass through.
func redactHighEntropyValue(match string) string {
	m := quotedAssignValueRe.FindStringSubmatch(match)
	sep, open, val, close := m[1], m[2], m[3], m[4]
	if open != close || !hasLetterAndDigit(val) {
		return match
	}
	return sep + open + redactedPlaceholder + close
}

// hasLetterAndDigit reports whether s contains at least one letter and at
// least one digit, the minimal entropy bar for the quoted-literal rule.
func hasLetterAndDigit(s string) bool {
	var letter, digit bool
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			letter = true
		case unicode.IsDigit(r):
			digit = true
		}
		if letter && digit {
			return true
		}
	}
	return false
}
