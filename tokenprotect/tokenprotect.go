// Package tokenprotect shields syntax that translation providers must preserve.
package tokenprotect

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind identifies a protected construct.
type Kind string

const (
	KindURL        Kind = "URL"
	KindPatchouli  Kind = "Patchouli macro"
	KindVariable   Kind = "variable"
	KindPrintf     Kind = "printf placeholder"
	KindFormatting Kind = "Minecraft formatting code"
	KindNewline    Kind = "newline"
	KindResource   Kind = "resource identifier"
)

// Token describes one occurrence in the source. Start and End are byte offsets.
type Token struct {
	Kind       Kind
	Text       string
	Start, End int
}

type protectedToken struct {
	Token
	marker string
}

// Text is a protected translation payload and retains the data needed to restore it.
type Text struct {
	Protected string
	tag       string
	tokens    []protectedToken
}

// Error is a provider-safe validation error. It intentionally does not include source tokens.
type Error struct {
	Problem string
	Marker  string
	Offset  int
}

func (e *Error) Error() string {
	if e.Marker != "" {
		return fmt.Sprintf("translation token validation failed at byte %d: %s (%s)", e.Offset, e.Problem, e.Marker)
	}
	return fmt.Sprintf("translation token validation failed: %s", e.Problem)
}

// Find returns protected constructs in source order. Recognition precedence is URL,
// Patchouli macro, variable, printf, formatting/newline, then resource identifier.
func Find(source string) []Token {
	var tokens []Token
	for i := 0; i < len(source); {
		end, kind := recognize(source, i)
		if end > i {
			tokens = append(tokens, Token{Kind: kind, Text: source[i:end], Start: i, End: end})
			i = end
			continue
		}
		if end := colonFreeNamespaceEnd(source, i); end > i {
			i = end
			continue
		}
		_, size := utf8.DecodeRuneInString(source[i:])
		if size == 0 {
			size = 1
		}
		i += size
	}
	return tokens
}

// Protect replaces tokens with short XML-style placeholders. A scoped tag is
// derived from the complete source when the default tag would collide with it.
func Protect(source string) (*Text, error) {
	tokens := Find(source)
	tag, err := placeholderTag(source)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	out.Grow(len(source))
	protected := make([]protectedToken, 0, len(tokens))
	last := 0
	for i, token := range tokens {
		marker := fmt.Sprintf(`<%s id="%d"/>`, tag, i)
		out.WriteString(source[last:token.Start])
		out.WriteString(marker)
		protected = append(protected, protectedToken{
			Token:  token,
			marker: marker,
		})
		last = token.End
	}
	out.WriteString(source[last:])
	return &Text{Protected: out.String(), tag: tag, tokens: protected}, nil
}

// Tokens returns a copy of the protected source token metadata.
func (t *Text) Tokens() []Token {
	result := make([]Token, len(t.tokens))
	for i := range t.tokens {
		result[i] = t.tokens[i].Token
	}
	return result
}

// Restore validates placeholder identity, count, syntax, and relative ordering
// before restoring the exact source tokens.
func (t *Text) Restore(translated string) (string, error) {
	seen := make([]bool, len(t.tokens))
	positions := make([]int, len(t.tokens))
	byMarker := make(map[string]int, len(t.tokens))
	for i := range t.tokens {
		byMarker[t.tokens[i].marker] = i
	}
	type occurrence struct {
		start, end, index int
	}
	occurrences := make([]occurrence, 0, len(t.tokens))
	for i := range positions {
		positions[i] = -1
	}

	rawStart := "<" + t.tag
	escapedStart := "&lt;" + t.tag
	for at := 0; ; {
		rawRel := exactTagStart(translated[at:], rawStart)
		escapedRel := exactTagStart(translated[at:], escapedStart)
		if rawRel < 0 && escapedRel < 0 {
			break
		}
		if escapedRel >= 0 && (rawRel < 0 || escapedRel < rawRel) {
			start := at + escapedRel
			return "", &Error{Problem: "escaped placeholder", Marker: safePlaceholder(translated[start:]), Offset: start}
		}
		start := at + rawRel
		end := start + len(rawStart)
		if end >= len(translated) || translated[end] != ' ' || !strings.HasPrefix(translated[end:], ` id="`) {
			return "", &Error{Problem: "malformed placeholder", Marker: safePlaceholder(translated[start:]), Offset: start}
		}
		end += len(` id="`)
		digitStart := end
		for end < len(translated) && translated[end] >= '0' && translated[end] <= '9' {
			end++
		}
		if end == digitStart || end+3 > len(translated) || translated[end:end+3] != `"/>` {
			return "", &Error{Problem: "malformed placeholder", Marker: safePlaceholder(translated[start:]), Offset: start}
		}
		marker := translated[start : end+3]
		index, known := byMarker[marker]
		if !known {
			return "", &Error{Problem: "unknown placeholder", Marker: marker, Offset: start}
		}
		if seen[index] {
			return "", &Error{Problem: "duplicated placeholder", Marker: marker, Offset: start}
		}
		seen[index] = true
		positions[index] = start
		occurrences = append(occurrences, occurrence{start: start, end: end + 3, index: index})
		at = end + 3
	}

	for i, ok := range seen {
		if !ok {
			return "", &Error{Problem: "missing placeholder", Marker: t.tokens[i].marker, Offset: -1}
		}
	}
	for i := 1; i < len(t.tokens); i++ {
		if positions[i] < positions[i-1] {
			return "", &Error{Problem: "placeholders were reordered", Marker: t.tokens[i].marker, Offset: positions[i]}
		}
	}

	var result strings.Builder
	result.Grow(len(translated))
	last := 0
	for _, occurrence := range occurrences {
		result.WriteString(translated[last:occurrence.start])
		result.WriteString(t.tokens[occurrence.index].Text)
		last = occurrence.end
	}
	result.WriteString(translated[last:])
	return result.String(), nil
}

func placeholderTag(source string) (string, error) {
	if !placeholderTagCollides(source, "keep") {
		return "keep", nil
	}
	for nonce := 0; nonce < 256; nonce++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("tokenprotect:%d:%s", nonce, source)))
		tag := fmt.Sprintf("keep-%x", digest[:4])
		if !placeholderTagCollides(source, tag) {
			return tag, nil
		}
	}
	return "", &Error{Problem: "could not derive a collision-free placeholder tag", Offset: -1}
}

func placeholderTagCollides(source, tag string) bool {
	return exactTagStart(source, "<"+tag) >= 0 || exactTagStart(source, "&lt;"+tag) >= 0
}

func exactTagStart(value, start string) int {
	for offset := 0; offset < len(value); {
		rel := strings.Index(value[offset:], start)
		if rel < 0 {
			return -1
		}
		at := offset + rel
		after := at + len(start)
		if after == len(value) || !isXMLNameByte(value[after]) {
			return at
		}
		offset = after
	}
	return -1
}

func isXMLNameByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("_.:-", rune(value))
}

func safePlaceholder(value string) string {
	end := 0
	for end < len(value) && end < 64 && value[end] != '>' && value[end] != '\n' && value[end] != '\r' {
		end++
	}
	if end < len(value) && value[end] == '>' {
		end++
	}
	return value[:end]
}

func recognize(s string, i int) (int, Kind) {
	if end := recognizeURL(s, i); end > i {
		return end, KindURL
	}
	if end := recognizePatchouli(s, i); end > i {
		return end, KindPatchouli
	}
	if end := recognizeVariable(s, i); end > i {
		return end, KindVariable
	}
	if end := recognizePrintf(s, i); end > i {
		return end, KindPrintf
	}
	if i+2 <= len(s) && s[i] == 0xc2 && s[i+1] == 0xa7 && i+3 <= len(s) && isFormatCode(s[i+2]) {
		return i + 3, KindFormatting
	}
	if i+1 < len(s) && s[i] == '&' && isFormatCode(s[i+1]) && !looksLikeOrdinaryAmpersand(s, i) {
		return i + 2, KindFormatting
	}
	if i+2 <= len(s) && s[i] == '\\' && (s[i+1] == 'n' || s[i+1] == 'r') {
		return i + 2, KindNewline
	}
	if s[i] == '\n' || s[i] == '\r' {
		if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
			return i + 2, KindNewline
		}
		return i + 1, KindNewline
	}
	if end := recognizeResource(s, i); end > i {
		return end, KindResource
	}
	return i, ""
}

func recognizeURL(s string, i int) int {
	if !(strings.HasPrefix(s[i:], "https://") || strings.HasPrefix(s[i:], "http://")) {
		return i
	}
	end := i
	for end < len(s) && !unicode.IsSpace(rune(s[end])) && !strings.ContainsRune("<>\"'", rune(s[end])) {
		end++
	}
	for end > i && strings.ContainsRune(".,;!?)]}", rune(s[end-1])) {
		end--
	}
	if end <= i+8 {
		return i
	}
	return end
}

func recognizePatchouli(s string, i int) int {
	if !strings.HasPrefix(s[i:], "$(") {
		return i
	}
	limit := i + 258
	if limit > len(s) {
		limit = len(s)
	}
	for j := i + 2; j < limit; j++ {
		if s[j] == ')' {
			return j + 1
		}
		if unicode.IsSpace(rune(s[j])) || s[j] == '(' {
			return i
		}
	}
	return i
}

func recognizeVariable(s string, i int) int {
	start := i
	if strings.HasPrefix(s[i:], "${") {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return start
	}
	end := i + 1
	for end < len(s) && (isASCIIAlphaNum(s[end]) || s[end] == '_') {
		end++
	}
	if end == i+1 || end >= len(s) || s[end] != '}' {
		return start
	}
	return end + 1
}

func recognizePrintf(s string, i int) int {
	if s[i] != '%' || i+1 >= len(s) {
		return i
	}
	if s[i+1] == '%' {
		return i + 2
	}
	j := i + 1
	indexStart := j
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j > indexStart && j < len(s) && s[j] == '$' {
		j++
	} else {
		j = i + 1
	}
	for j < len(s) && strings.ContainsRune("-+# 0,(<", rune(s[j])) {
		j++
	}
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j < len(s) && s[j] == '.' {
		j++
		for j < len(s) && isDigit(s[j]) {
			j++
		}
	}
	if j >= len(s) {
		return i
	}
	if s[j] == 't' || s[j] == 'T' {
		if j+1 < len(s) && strings.ContainsRune("HIklMSLNpzZsQBbhAaCYyjmdeRTrDFc", rune(s[j+1])) {
			return j + 2
		}
		return i
	}
	if strings.ContainsRune("bBhHsScCdoxXeEfgGaAn", rune(s[j])) {
		return j + 1
	}
	return i
}

func isIndexedPrintf(s string) bool {
	if len(s) < 4 || s[0] != '%' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '$' {
			return i > 1
		}
		if !isDigit(s[i]) {
			return false
		}
	}
	return false
}

func recognizeResource(s string, i int) int {
	if i > 0 && isResourceChar(s[i-1]) {
		return i
	}
	j := i
	for j < len(s) && isNamespaceChar(s[j]) {
		j++
	}
	if j == i || j >= len(s) || s[j] != ':' {
		return i
	}
	j++
	pathStart := j
	for j < len(s) && isResourceChar(s[j]) {
		j++
	}
	if j == pathStart || continuesIdentifier(s, j) {
		return i
	}
	return j
}

func colonFreeNamespaceEnd(s string, i int) int {
	if !isNamespaceChar(s[i]) || i > 0 && isResourceChar(s[i-1]) {
		return i
	}
	j := i
	for j < len(s) && isNamespaceChar(s[j]) {
		j++
	}
	if j == len(s) || s[j] != ':' {
		return j
	}
	return i
}

func continuesIdentifier(s string, i int) bool {
	if i >= len(s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isNamespaceChar(b byte) bool {
	return b >= 'a' && b <= 'z' || isDigit(b) || b == '_' || b == '-' || b == '.'
}
func isResourceChar(b byte) bool  { return isNamespaceChar(b) || b == '/' }
func isDigit(b byte) bool         { return b >= '0' && b <= '9' }
func isASCIIAlphaNum(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || isDigit(b) }
func isASCIILower(b byte) bool    { return b >= 'a' && b <= 'z' }
func isASCIIUpper(b byte) bool    { return b >= 'A' && b <= 'Z' }
func isFormatCode(b byte) bool {
	b |= 0x20
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || strings.ContainsRune("klmnor", rune(b))
}

func looksLikeOrdinaryAmpersand(s string, i int) bool {
	code := s[i+1]
	after := byte(0)
	if i+2 < len(s) {
		after = s[i+2]
	}
	if isASCIILower(code) && isASCIILower(after) {
		end := i + 2
		for end < len(s) && isASCIILower(s[end]) {
			end++
		}
		if end < len(s) && s[end] == ';' {
			return true
		}
	}
	if i > 0 && isASCIIUpper(s[i-1]) && isASCIIUpper(code) && (after == 0 || !isASCIIAlphaNum(after)) {
		return true
	}
	return false
}
