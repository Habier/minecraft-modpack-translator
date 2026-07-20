package main

import (
	"fmt"
	"strconv"
	"strings"
)

// normalizeFTBQuestSNBT accepts the FTB Quests separator dialect while validating
// enough SNBT structure to insert commas only between complete sibling values.
func normalizeFTBQuestSNBT(data []byte) ([]byte, error) {
	if len(data) > maxPendingSourceFileSize {
		return nil, fmt.Errorf("source is %d bytes; limit is %d", len(data), maxPendingSourceFileSize)
	}
	growth := len(data) / 2
	if len(data) > int(^uint(0)>>1)-growth {
		return nil, fmt.Errorf("normalized size overflow at byte 0 (line 1, column 1)")
	}
	p := ftbSNBTNormalizer{data: data, out: make([]byte, 0, len(data)+growth), maxOutput: len(data) + growth}
	p.space()
	if err := p.value(1); err != nil {
		return nil, err
	}
	p.space()
	if p.at != len(data) {
		return nil, p.fail(p.at, "unexpected data")
	}
	return p.out, nil
}

type ftbSNBTNormalizer struct {
	data      []byte
	out       []byte
	at        int
	maxOutput int
}

func (p *ftbSNBTNormalizer) value(depth int) error {
	if depth > maxCatalogDepth {
		return p.fail(p.at, fmt.Sprintf("nesting exceeds %d", maxCatalogDepth))
	}
	p.space()
	if p.at >= len(p.data) {
		return p.fail(p.at, "unexpected end; expected value")
	}
	switch p.data[p.at] {
	case '{':
		return p.compound(depth)
	case '[':
		return p.list(depth)
	case '\'', '"':
		return p.quoted()
	case '}', ']', ',', ':':
		return p.fail(p.at, "expected value")
	default:
		return p.bareValue()
	}
}

func (p *ftbSNBTNormalizer) compound(depth int) error {
	p.copyByte()
	p.space()
	if p.take('}') {
		return nil
	}
	for count := 1; ; count++ {
		if count > maxCatalogMembers {
			return p.fail(p.at, fmt.Sprintf("compound exceeds %d members", maxCatalogMembers))
		}
		if err := p.key(); err != nil {
			return err
		}
		p.space()
		if !p.take(':') {
			return p.fail(p.at, "expected colon after compound key")
		}
		if err := p.value(depth + 1); err != nil {
			return err
		}
		spaceAt := p.at
		p.space()
		if p.take('}') {
			return nil
		}
		if p.take(',') {
			p.space()
			if p.take('}') {
				return nil
			}
			continue
		}
		if spaceAt < p.at && p.compoundKeyAhead(p.at) {
			if len(p.out) >= p.maxOutput {
				return p.fail(p.at, "normalized output growth limit exceeded")
			}
			p.out = append(p.out, ',')
			continue
		}
		return p.fail(p.at, "expected comma between compound members")
	}
}

func (p *ftbSNBTNormalizer) list(depth int) error {
	p.copyByte()
	p.space()
	if p.take(']') {
		return nil
	}
	if p.at+1 < len(p.data) && strings.ContainsRune("BIL", rune(p.data[p.at])) && p.data[p.at+1] == ';' {
		kind := p.data[p.at]
		p.copyByte()
		p.copyByte()
		return p.listElements(depth, kind)
	}
	return p.listElements(depth, 0)
}

func (p *ftbSNBTNormalizer) listElements(depth int, typedKind byte) error {
	p.space()
	if p.take(']') {
		return nil
	}
	for count := 1; ; count++ {
		if count > maxCatalogMembers {
			return p.fail(p.at, fmt.Sprintf("list exceeds %d members", maxCatalogMembers))
		}
		wasQuoted := typedKind == 0 && p.at < len(p.data) && (p.data[p.at] == '\'' || p.data[p.at] == '"')
		typedValueValid := false
		if typedKind != 0 {
			var err error
			typedValueValid, err = p.typedArrayValue(typedKind)
			if err != nil {
				return err
			}
		} else if err := p.value(depth + 1); err != nil {
			return err
		}
		wasCompound := typedKind == 0 && p.at > 0 && p.data[p.at-1] == '}'
		spaceAt := p.at
		p.space()
		if p.take(']') {
			return nil
		}
		if p.take(',') {
			p.space()
			if p.take(']') {
				return nil
			}
			continue
		}
		if wasCompound && p.at < len(p.data) && p.data[p.at] == '{' {
			if len(p.out) >= p.maxOutput {
				return p.fail(p.at, "normalized output growth limit exceeded")
			}
			p.out = append(p.out, ',')
			continue
		}
		if wasQuoted && spaceAt < p.at && p.at < len(p.data) && (p.data[p.at] == '\'' || p.data[p.at] == '"') {
			if len(p.out) >= p.maxOutput {
				return p.fail(p.at, "normalized output growth limit exceeded")
			}
			p.out = append(p.out, ',')
			continue
		}
		if typedValueValid && spaceAt < p.at && p.typedArrayValueAhead(typedKind) {
			if len(p.out) >= p.maxOutput {
				return p.fail(p.at, "normalized output growth limit exceeded")
			}
			p.out = append(p.out, ',')
			continue
		}
		return p.fail(p.at, "expected comma between list elements")
	}
}

func (p *ftbSNBTNormalizer) typedArrayValue(kind byte) (bool, error) {
	p.space()
	if p.at >= len(p.data) || strings.ContainsRune("{}[],:;'\"", rune(p.data[p.at])) {
		return false, p.fail(p.at, "expected typed array value")
	}
	start := p.at
	if err := p.bare(); err != nil {
		return false, err
	}
	return validTypedArrayLiteral(p.data[start:p.at], kind), nil
}

func (p *ftbSNBTNormalizer) typedArrayValueAhead(kind byte) bool {
	end := p.at
	for end < len(p.data) && !isSNBTDelimiter(p.data[end]) {
		if !isSNBTBareByte(p.data[end]) {
			return false
		}
		end++
	}
	return end > p.at && validTypedArrayLiteral(p.data[p.at:end], kind)
}

func validTypedArrayLiteral(literal []byte, kind byte) bool {
	if len(literal) == 0 {
		return false
	}
	bits := 32
	number := literal
	switch kind {
	case 'B':
		bits = 8
		if suffix := literal[len(literal)-1]; suffix != 'b' && suffix != 'B' {
			return false
		}
		number = literal[:len(literal)-1]
	case 'I':
	case 'L':
		bits = 64
		if suffix := literal[len(literal)-1]; suffix != 'l' && suffix != 'L' {
			return false
		}
		number = literal[:len(literal)-1]
	default:
		return false
	}
	if len(number) == 0 || number[0] == '+' {
		return false
	}
	_, err := strconv.ParseInt(string(number), 10, bits)
	return err == nil
}

func (p *ftbSNBTNormalizer) key() error {
	p.space()
	if p.at >= len(p.data) {
		return p.fail(p.at, "unexpected end; expected compound key")
	}
	if p.data[p.at] == '\'' || p.data[p.at] == '"' {
		return p.quoted()
	}
	if strings.ContainsRune("{}[],:;", rune(p.data[p.at])) {
		return p.fail(p.at, "expected compound key")
	}
	return p.bare()
}

func (p *ftbSNBTNormalizer) compoundKeyAhead(at int) bool {
	if at >= len(p.data) {
		return false
	}
	if p.data[at] == '\'' || p.data[at] == '"' {
		quote := p.data[at]
		at++
		closed := false
		for at < len(p.data) {
			value := p.data[at]
			if value == '\n' || value == '\r' {
				return false
			}
			at++
			if value == quote {
				closed = true
				break
			}
			if value == '\\' {
				if at >= len(p.data) {
					return false
				}
				at++
			}
		}
		if !closed {
			return false
		}
	} else {
		start := at
		for at < len(p.data) && isSNBTBareByte(p.data[at]) {
			at++
		}
		if at == start {
			return false
		}
	}
	for at < len(p.data) && isSNBTWhitespace(p.data[at]) {
		at++
	}
	return at < len(p.data) && p.data[at] == ':'
}

func (p *ftbSNBTNormalizer) quoted() error {
	quote := p.data[p.at]
	p.copyByte()
	for p.at < len(p.data) {
		value := p.data[p.at]
		if value == '\n' || value == '\r' {
			return p.fail(p.at, "newline in quoted string")
		}
		p.copyByte()
		if value == quote {
			return nil
		}
		if value == '\\' {
			if p.at >= len(p.data) {
				return p.fail(p.at, "unterminated string escape")
			}
			p.copyByte()
		}
	}
	return p.fail(p.at, "unterminated quoted string")
}

func (p *ftbSNBTNormalizer) bare() error {
	start := p.at
	for p.at < len(p.data) && !isSNBTDelimiter(p.data[p.at]) {
		if p.at+1 < len(p.data) && p.data[p.at] == '/' && (p.data[p.at+1] == '/' || p.data[p.at+1] == '*') {
			return p.fail(p.at, "comments are not supported in FTB quest SNBT")
		}
		if !isSNBTBareByte(p.data[p.at]) {
			return p.fail(p.at, "unsupported character in unquoted token")
		}
		p.copyByte()
	}
	if p.at == start {
		return p.fail(p.at, "expected token")
	}
	return nil
}

func (p *ftbSNBTNormalizer) bareValue() error {
	start := p.at
	for p.at < len(p.data) && !strings.ContainsRune("{}[],; \t\r\n\v\f", rune(p.data[p.at])) {
		if p.at+1 < len(p.data) && p.data[p.at] == '/' && (p.data[p.at+1] == '/' || p.data[p.at+1] == '*') {
			return p.fail(p.at, "comments are not supported in FTB quest SNBT")
		}
		if !isSNBTBareValueByte(p.data[p.at]) {
			return p.fail(p.at, "unsupported character in unquoted token")
		}
		p.copyByte()
	}
	if p.at == start {
		return p.fail(p.at, "expected token")
	}
	return nil
}

func isSNBTDelimiter(value byte) bool {
	return strings.ContainsRune("{}[],:; \t\r\n\v\f", rune(value))
}

func isSNBTBareByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("._+-", rune(value))
}

func isSNBTBareValueByte(value byte) bool {
	return isSNBTBareByte(value) || value == ':' || value == '#'
}

func isSNBTWhitespace(value byte) bool {
	return strings.ContainsRune(" \t\r\n\v\f", rune(value))
}

func (p *ftbSNBTNormalizer) space() {
	for p.at < len(p.data) && isSNBTWhitespace(p.data[p.at]) {
		p.copyByte()
	}
}

func (p *ftbSNBTNormalizer) take(value byte) bool {
	if p.at < len(p.data) && p.data[p.at] == value {
		p.copyByte()
		return true
	}
	return false
}

func (p *ftbSNBTNormalizer) copyByte() {
	p.out = append(p.out, p.data[p.at])
	p.at++
}

func (p *ftbSNBTNormalizer) fail(offset int, message string) error {
	line, column := 1, 1
	for i := 0; i < offset && i < len(p.data); i++ {
		if p.data[i] == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return fmt.Errorf("%s at byte %d (line %d, column %d)", message, offset, line, column)
}
