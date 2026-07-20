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
	marker       string
	orderedGroup string
}

// Text is a protected translation payload and retains the data needed to restore it.
type Text struct {
	Protected string
	prefix    string
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

// Protect replaces tokens with deterministic opaque markers. The marker prefix is
// derived from the complete source and re-derived if it collides with source text.
func Protect(source string) (*Text, error) {
	tokens := Find(source)
	prefix, err := markerPrefix(source)
	if err != nil {
		return nil, err
	}

	var out strings.Builder
	out.Grow(len(source))
	protected := make([]protectedToken, 0, len(tokens))
	last := 0
	for i, token := range tokens {
		marker := fmt.Sprintf("%s%06d__", prefix, i)
		out.WriteString(source[last:token.Start])
		out.WriteString(marker)
		protected = append(protected, protectedToken{
			Token:        token,
			marker:       marker,
			orderedGroup: orderGroup(token),
		})
		last = token.End
	}
	out.WriteString(source[last:])
	return &Text{Protected: out.String(), prefix: prefix, tokens: protected}, nil
}

// Tokens returns a copy of the protected source token metadata.
func (t *Text) Tokens() []Token {
	result := make([]Token, len(t.tokens))
	for i := range t.tokens {
		result[i] = t.tokens[i].Token
	}
	return result
}

// Restore validates marker identity, count, syntax, and required ordering before
// restoring the exact source tokens. Provider output may otherwise move markers.
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

	for at := 0; ; {
		rel := strings.Index(translated[at:], t.prefix)
		if rel < 0 {
			break
		}
		start := at + rel
		end := start + len(t.prefix)
		for end < len(translated) && translated[end] >= '0' && translated[end] <= '9' {
			end++
		}
		if end == start+len(t.prefix) || end+2 > len(translated) || translated[end:end+2] != "__" {
			return "", &Error{Problem: "malformed marker", Marker: safeMarker(translated[start:], t.prefix), Offset: start}
		}
		marker := translated[start : end+2]
		index, known := byMarker[marker]
		if !known {
			return "", &Error{Problem: "unknown marker", Marker: marker, Offset: start}
		}
		if seen[index] {
			return "", &Error{Problem: "duplicated marker", Marker: marker, Offset: start}
		}
		seen[index] = true
		positions[index] = start
		occurrences = append(occurrences, occurrence{start: start, end: end + 2, index: index})
		at = end + 2
	}

	for i, ok := range seen {
		if !ok {
			return "", &Error{Problem: "missing marker", Marker: t.tokens[i].marker, Offset: -1}
		}
	}
	lastByGroup := map[string]int{}
	for i, token := range t.tokens {
		if token.orderedGroup == "" {
			continue
		}
		if previous, ok := lastByGroup[token.orderedGroup]; ok && positions[i] < previous {
			return "", &Error{Problem: token.orderedGroup + " markers were reordered", Marker: token.marker, Offset: positions[i]}
		}
		lastByGroup[token.orderedGroup] = positions[i]
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

func markerPrefix(source string) (string, error) {
	for nonce := 0; nonce < 256; nonce++ {
		digest := sha256.Sum256([]byte(fmt.Sprintf("tokenprotect:%d:%s", nonce, source)))
		prefix := fmt.Sprintf("__MPT_%x_", digest[:8])
		if !strings.Contains(source, prefix) {
			return prefix, nil
		}
	}
	return "", &Error{Problem: "could not derive a collision-free marker prefix", Offset: -1}
}

func safeMarker(value, prefix string) string {
	end := len(prefix)
	for end < len(value) && end < len(prefix)+24 && value[end] > ' ' {
		end++
	}
	return value[:end]
}

func orderGroup(token Token) string {
	if token.Kind == KindPatchouli {
		return "Patchouli"
	}
	if token.Kind == KindPrintf && !isIndexedPrintf(token.Text) && token.Text != "%%" {
		return "unindexed printf"
	}
	if token.Kind == KindFormatting {
		return "Minecraft formatting"
	}
	return ""
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
func isFormatCode(b byte) bool {
	b |= 0x20
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || strings.ContainsRune("klmnor", rune(b))
}
