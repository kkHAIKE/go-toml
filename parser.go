package toml

import (
	"bytes"
	"strconv"

	"github.com/pelletier/go-toml/v2/internal/ast"
	"github.com/pelletier/go-toml/v2/internal/danger"
)

type Decoration struct {
	Before  []byte
	After   []byte
	Comment []byte
}

type parser struct {
	builder ast.Builder
	ref     ast.Reference
	data    []byte
	left    []byte
	err     error
	first   bool

	xast bool
	decm map[ast.Reference]*Decoration
}

func (p *parser) Range(b []byte) ast.Range {
	return ast.Range{
		Offset: uint32(danger.SubsliceOffset(p.data, b)),
		Length: uint32(len(b)),
	}
}

func (p *parser) Raw(raw ast.Range) []byte {
	return p.data[raw.Offset : raw.Offset+raw.Length]
}

func (p *parser) Reset(b []byte) {
	p.builder.Reset()
	p.ref = ast.InvalidReference
	p.data = b
	p.left = b
	p.err = nil
	p.first = true
	if p.xast {
		p.decm = make(map[ast.Reference]*Decoration)
	}
}

//nolint:cyclop
func (p *parser) NextExpression() bool {
	if len(p.left) == 0 || p.err != nil {
		return false
	}

	p.builder.Reset()
	p.ref = ast.InvalidReference

	for {
		if len(p.left) == 0 || p.err != nil {
			return false
		}

		if !p.first {
			p.left, p.err = p.parseNewline(p.left)
		}

		if len(p.left) == 0 || p.err != nil {
			return false
		}

		p.ref, p.left, p.err = p.parseExpression(p.left)

		if p.err != nil {
			return false
		}

		p.first = false

		if p.ref.Valid() {
			return true
		}
	}
}

func (p *parser) Expression() *ast.Node {
	return p.builder.NodeAt(p.ref)
}

func (p *parser) Error() error {
	return p.err
}

func (p *parser) parseNewline(b []byte) ([]byte, error) {
	if b[0] == '\n' {
		return b[1:], nil
	}

	if b[0] == '\r' {
		_, rest, err := scanWindowsNewline(b)
		return rest, err
	}

	return nil, newDecodeError(b[0:1], "expected newline but got %#U", b[0])
}

func (p *parser) parseExpression(b []byte) (ast.Reference, []byte, error) {
	// expression =  ws [ comment ]
	// expression =/ ws keyval ws [ comment ]
	// expression =/ ws table ws [ comment ]
	ref := ast.InvalidReference

	ws, b := scanWhitespace(b)

	if len(b) == 0 {
		if p.xast && len(ws) > 0 {
			ref = p.builder.Push(ast.Node{
				Kind: ast.WhiteSpace,
				Data: ws,
			})
		}
		return ref, b, nil
	}

	if b[0] == '#' {
		com, rest := scanComment(b)
		if p.xast {
			ref = p.builder.Push(ast.Node{
				Kind: ast.Comment,
				Data: com,
			})
			if len(ws) > 0 {
				p.decm[ref] = &Decoration{Before: ws}
			}
		}

		return ref, rest, nil
	}

	if b[0] == '\n' || b[0] == '\r' {
		if p.xast {
			ref = p.builder.Push(ast.Node{
				Kind: ast.WhiteSpace,
				Data: ws,
			})
		}
		return ref, b, nil
	}

	var err error
	if b[0] == '[' {
		ref, b, err = p.parseTable(b)
	} else {
		ref, b, err = p.parseKeyval(b)
	}

	if err != nil {
		return ref, nil, err
	}

	ws2, b := scanWhitespace(b)
	if p.xast && (len(ws) > 0 || len(ws2) > 0) {
		p.decm[ref] = &Decoration{
			Before: ws,
			After:  ws2,
		}
	}

	if len(b) > 0 && b[0] == '#' {
		com, rest := scanComment(b)
		if p.xast {
			if v, ok := p.decm[ref]; ok {
				v.Comment = com
			} else {
				p.decm[ref] = &Decoration{Comment: com}
			}
		}

		return ref, rest, nil
	}

	return ref, b, nil
}

func (p *parser) parseTable(b []byte) (ast.Reference, []byte, error) {
	// table = std-table / array-table
	if len(b) > 1 && b[1] == '[' {
		return p.parseArrayTable(b)
	}

	return p.parseStdTable(b)
}

func (p *parser) parseArrayTable(b []byte) (ast.Reference, []byte, error) {
	// array-table = array-table-open key array-table-close
	// array-table-open  = %x5B.5B ws  ; [[ Double left square bracket
	// array-table-close = ws %x5D.5D  ; ]] Double right square bracket
	ref := p.builder.Push(ast.Node{
		Kind: ast.ArrayTable,
	})
	start := uint32(danger.SubsliceOffset(p.data, b))

	b = b[2:]
	b = p.parseWhitespace(b)

	k, b, err := p.parseKey(b)
	if err != nil {
		return ref, nil, err
	}

	p.builder.AttachChild(ref, k)
	b = p.parseWhitespace(b)

	b, err = expect(']', b)
	if err != nil {
		return ref, nil, err
	}

	b, err = expect(']', b)

	if p.xast && err == nil {
		p.builder.NodeAt(ref).Raw = ast.Range{
			Offset: start,
			Length: uint32(danger.SubsliceOffset(p.data, b)) - start,
		}
	}

	return ref, b, err
}

func (p *parser) parseStdTable(b []byte) (ast.Reference, []byte, error) {
	// std-table = std-table-open key std-table-close
	// std-table-open  = %x5B ws     ; [ Left square bracket
	// std-table-close = ws %x5D     ; ] Right square bracket
	ref := p.builder.Push(ast.Node{
		Kind: ast.Table,
	})
	start := uint32(danger.SubsliceOffset(p.data, b))

	b = b[1:]
	b = p.parseWhitespace(b)

	key, b, err := p.parseKey(b)
	if err != nil {
		return ref, nil, err
	}

	p.builder.AttachChild(ref, key)

	b = p.parseWhitespace(b)

	b, err = expect(']', b)

	if p.xast && err == nil {
		p.builder.NodeAt(ref).Raw = ast.Range{
			Offset: start,
			Length: uint32(danger.SubsliceOffset(p.data, b)) - start,
		}
	}

	return ref, b, err
}

func (p *parser) parseKeyval(b []byte) (ast.Reference, []byte, error) {
	// keyval = key keyval-sep val
	ref := p.builder.Push(ast.Node{
		Kind: ast.KeyValue,
	})

	key, b, err := p.parseKey(b)
	if err != nil {
		return ast.InvalidReference, nil, err
	}

	// keyval-sep = ws %x3D ws ; =

	ws, b := scanWhitespace(b)

	if len(b) == 0 {
		return ast.InvalidReference, nil, newDecodeError(b, "expected = after a key, but the document ends there")
	}

	equal := b[:1]
	b, err = expect('=', b)
	if err != nil {
		return ast.InvalidReference, nil, err
	}

	ws2, b := scanWhitespace(b)

	if p.xast {
		sym := p.builder.Push(ast.Node{
			Kind: ast.Symbol,
			Data: equal,
		})
		p.builder.AttachChild(key, sym)
		if len(ws) > 0 || len(ws2) > 0 {
			p.decm[sym] = &Decoration{
				Before: ws,
				After:  ws2,
			}
		}
	}

	valRef, b, err := p.parseVal(b)
	if err != nil {
		return ref, b, err
	}

	p.builder.Chain(valRef, key)
	p.builder.AttachChild(ref, valRef)

	return ref, b, err
}

//nolint:cyclop,funlen
func (p *parser) parseVal(b []byte) (ast.Reference, []byte, error) {
	// val = string / boolean / array / inline-table / date-time / float / integer
	ref := ast.InvalidReference

	if len(b) == 0 {
		return ref, nil, newDecodeError(b, "expected value, not eof")
	}

	var err error
	c := b[0]

	switch c {
	case '"':
		var raw []byte
		var v []byte
		if scanFollowsMultilineBasicStringDelimiter(b) {
			raw, v, b, err = p.parseMultilineBasicString(b)
		} else {
			raw, v, b, err = p.parseBasicString(b)
		}

		if err == nil {
			ref = p.builder.Push(ast.Node{
				Kind: ast.String,
				Raw:  p.Range(raw),
				Data: v,
			})
		}

		return ref, b, err
	case '\'':
		var raw []byte
		var v []byte
		if scanFollowsMultilineLiteralStringDelimiter(b) {
			raw, v, b, err = p.parseMultilineLiteralString(b)
		} else {
			raw, v, b, err = p.parseLiteralString(b)
		}

		if err == nil {
			ref = p.builder.Push(ast.Node{
				Kind: ast.String,
				Raw:  p.Range(raw),
				Data: v,
			})
		}

		return ref, b, err
	case 't':
		if !scanFollowsTrue(b) {
			return ref, nil, newDecodeError(atmost(b, 4), "expected 'true'")
		}

		ref = p.builder.Push(ast.Node{
			Kind: ast.Bool,
			Data: b[:4],
		})

		return ref, b[4:], nil
	case 'f':
		if !scanFollowsFalse(b) {
			return ref, nil, newDecodeError(atmost(b, 5), "expected 'false'")
		}

		ref = p.builder.Push(ast.Node{
			Kind: ast.Bool,
			Data: b[:5],
		})

		return ref, b[5:], nil
	case '[':
		return p.parseValArray(b)
	case '{':
		return p.parseInlineTable(b)
	default:
		return p.parseIntOrFloatOrDateTime(b)
	}
}

func atmost(b []byte, n int) []byte {
	if n >= len(b) {
		return b
	}

	return b[:n]
}

func (p *parser) parseLiteralString(b []byte) ([]byte, []byte, []byte, error) {
	v, rest, err := scanLiteralString(b)
	if err != nil {
		return nil, nil, nil, err
	}

	return v, v[1 : len(v)-1], rest, nil
}

func (p *parser) parseInlineTable(b []byte) (ast.Reference, []byte, error) {
	// inline-table = inline-table-open [ inline-table-keyvals ] inline-table-close
	// inline-table-open  = %x7B ws     ; {
	// inline-table-close = ws %x7D     ; }
	// inline-table-sep   = ws %x2C ws  ; , Comma
	// inline-table-keyvals = keyval [ inline-table-sep inline-table-keyvals ]
	parent := p.builder.Push(ast.Node{
		Kind: ast.InlineTable,
	})
	start := uint32(danger.SubsliceOffset(p.data, b))

	first := true

	var child ast.Reference

	b = b[1:]

	var err error

	for len(b) > 0 {
		var ws []byte
		ws, b = scanWhitespace(b)
		if b[0] == '}' {
			break
		}

		if !first {
			comma := b[:1]
			b, err = expect(',', b)
			if err != nil {
				return parent, nil, err
			}
			var ws2 []byte
			ws2, b = scanWhitespace(b)

			if p.xast {
				sym := p.builder.Push(ast.Node{
					Kind: ast.Symbol,
					Data: comma,
				})
				p.builder.Chain(child, sym)
				child = sym

				if len(ws) > 0 || len(ws2) > 0 {
					p.decm[sym] = &Decoration{
						Before: ws,
						After:  ws2,
					}
				}
			}
		}

		var kv ast.Reference

		kv, b, err = p.parseKeyval(b)
		if err != nil {
			return parent, nil, err
		}

		if first {
			p.builder.AttachChild(parent, kv)
		} else {
			p.builder.Chain(child, kv)
		}
		child = kv

		first = false
	}

	rest, err := expect('}', b)

	if p.xast && err == nil {
		p.builder.NodeAt(parent).Raw = ast.Range{
			Offset: start,
			Length: uint32(danger.SubsliceOffset(p.data, rest)) - start,
		}
	}

	return parent, rest, err
}

//nolint:funlen,cyclop
func (p *parser) parseValArray(b []byte) (ast.Reference, []byte, error) {
	// array = array-open [ array-values ] ws-comment-newline array-close
	// array-open =  %x5B ; [
	// array-close = %x5D ; ]
	// array-values =  ws-comment-newline val ws-comment-newline array-sep array-values
	// array-values =/ ws-comment-newline val ws-comment-newline [ array-sep ]
	// array-sep = %x2C  ; , Comma
	// ws-comment-newline = *( wschar / [ comment ] newline )
	start := uint32(danger.SubsliceOffset(p.data, b))
	b = b[1:]

	parent := p.builder.Push(ast.Node{
		Kind: ast.Array,
	})

	first := true
	firstChild := true
	prevVal := false

	var lastChild ast.Reference

	var err error
	for len(b) > 0 {
		var ws []byte
		b, ws, err = p.parseOptionalWhitespaceCommentNewline(b, parent, &firstChild, &lastChild, prevVal)
		prevVal = false
		if err != nil {
			return parent, nil, err
		}

		if len(b) == 0 {
			return parent, nil, newDecodeError(b, "array is incomplete")
		}

		if b[0] == ']' {
			break
		}

		if b[0] == ',' {
			if first {
				return parent, nil, newDecodeError(b[0:1], "array cannot start with comma")
			}

			if p.xast {
				ref := p.builder.Push(ast.Node{
					Kind: ast.Symbol,
					Data: b[:1],
				})
				if len(ws) > 0 {
					p.decm[ref] = &Decoration{Before: ws}
				}

				if firstChild {
					firstChild = false
					p.builder.AttachChild(parent, ref)
				} else {
					p.builder.Chain(lastChild, ref)
				}
				lastChild = ref
			}

			b = b[1:]

			b, ws, err = p.parseOptionalWhitespaceCommentNewline(b, parent, &firstChild, &lastChild, true)
			if err != nil {
				return parent, nil, err
			}
		}

		// TOML allows trailing commas in arrays.
		if len(b) > 0 && b[0] == ']' {
			break
		}

		var valueRef ast.Reference

		valueRef, b, err = p.parseVal(b)
		if err != nil {
			return parent, nil, err
		}

		if p.xast && len(ws) > 0 {
			p.decm[valueRef] = &Decoration{Before: ws}
		}

		if firstChild {
			p.builder.AttachChild(parent, valueRef)
		} else {
			p.builder.Chain(lastChild, valueRef)
		}
		lastChild = valueRef
		firstChild = false

		// b, err = p.parseOptionalWhitespaceCommentNewline(b)
		// if err != nil {
		// 	return parent, nil, err
		// }
		prevVal = true
		first = false
	}

	rest, err := expect(']', b)

	if p.xast && err == nil {
		p.builder.NodeAt(parent).Raw = ast.Range{
			Offset: start,
			Length: uint32(danger.SubsliceOffset(p.data, rest)) - start,
		}
	}

	return parent, rest, err
}

func (p *parser) parseOptionalWhitespaceCommentNewline(b []byte, parent ast.Reference, first *bool, lastChild *ast.Reference, after bool) ([]byte, []byte, error) {
	var ws []byte
	for len(b) > 0 {
		var err error
		ws, b = scanWhitespace(b)

		var com []byte
		if len(b) > 0 && b[0] == '#' {
			com, b = scanComment(b)
		}

		if len(b) == 0 {
			break
		}

		if b[0] == '\n' || b[0] == '\r' {
			b, err = p.parseNewline(b)
			if err != nil {
				return nil, nil, err
			}

			if p.xast {
				if after {
					after = false

					if len(ws) > 0 || len(com) > 0 {
						if v, ok := p.decm[*lastChild]; ok {
							v.After = ws
							v.Comment = com
						} else {
							p.decm[*lastChild] = &Decoration{
								After:   ws,
								Comment: com,
							}
						}
					}
				} else {
					var ref ast.Reference
					if len(com) > 0 {
						ref = p.builder.Push(ast.Node{
							Kind: ast.Comment,
							Data: com,
						})
						if len(ws) > 0 {
							p.decm[ref] = &Decoration{Before: ws}
						}
					} else {
						ref = p.builder.Push(ast.Node{
							Kind: ast.WhiteSpace,
							Data: ws,
						})
					}

					if *first {
						*first = false
						p.builder.AttachChild(parent, ref)
					} else {
						p.builder.Chain(*lastChild, ref)
					}
					*lastChild = ref
				}
			}

			ws = nil
		} else {
			break
		}
	}

	return b, ws, nil
}

func (p *parser) parseMultilineLiteralString(b []byte) ([]byte, []byte, []byte, error) {
	token, rest, err := scanMultilineLiteralString(b)
	if err != nil {
		return nil, nil, nil, err
	}

	i := 3

	// skip the immediate new line
	if token[i] == '\n' {
		i++
	} else if token[i] == '\r' && token[i+1] == '\n' {
		i += 2
	}

	return token, token[i : len(token)-3], rest, err
}

//nolint:funlen,gocognit,cyclop
func (p *parser) parseMultilineBasicString(b []byte) ([]byte, []byte, []byte, error) {
	// ml-basic-string = ml-basic-string-delim [ newline ] ml-basic-body
	// ml-basic-string-delim
	// ml-basic-string-delim = 3quotation-mark
	// ml-basic-body = *mlb-content *( mlb-quotes 1*mlb-content ) [ mlb-quotes ]
	//
	// mlb-content = mlb-char / newline / mlb-escaped-nl
	// mlb-char = mlb-unescaped / escaped
	// mlb-quotes = 1*2quotation-mark
	// mlb-unescaped = wschar / %x21 / %x23-5B / %x5D-7E / non-ascii
	// mlb-escaped-nl = escape ws newline *( wschar / newline )
	token, rest, err := scanMultilineBasicString(b)
	if err != nil {
		return nil, nil, nil, err
	}

	i := 3

	// skip the immediate new line
	if token[i] == '\n' {
		i++
	} else if token[i] == '\r' && token[i+1] == '\n' {
		i += 2
	}

	// fast path
	startIdx := i
	endIdx := len(token) - len(`"""`)
	for ; i < endIdx; i++ {
		if token[i] == '\\' {
			break
		}
	}
	if i == endIdx {
		return token, token[startIdx:endIdx], rest, nil
	}

	var builder bytes.Buffer
	builder.Write(token[startIdx:i])

	// The scanner ensures that the token starts and ends with quotes and that
	// escapes are balanced.
	for ; i < len(token)-3; i++ {
		c := token[i]

		//nolint:nestif
		if c == '\\' {
			// When the last non-whitespace character on a line is an unescaped \,
			// it will be trimmed along with all whitespace (including newlines) up
			// to the next non-whitespace character or closing delimiter.
			if token[i+1] == '\n' || (token[i+1] == '\r' && token[i+2] == '\n') {
				i++ // skip the \
				for ; i < len(token)-3; i++ {
					c := token[i]
					if !(c == '\n' || c == '\r' || c == ' ' || c == '\t') {
						i--

						break
					}
				}

				continue
			}

			// handle escaping
			i++
			c = token[i]

			switch c {
			case '"', '\\':
				builder.WriteByte(c)
			case 'b':
				builder.WriteByte('\b')
			case 'f':
				builder.WriteByte('\f')
			case 'n':
				builder.WriteByte('\n')
			case 'r':
				builder.WriteByte('\r')
			case 't':
				builder.WriteByte('\t')
			case 'u':
				x, err := hexToString(atmost(token[i+1:], 4), 4)
				if err != nil {
					return nil, nil, nil, err
				}

				builder.WriteString(x)
				i += 4
			case 'U':
				x, err := hexToString(atmost(token[i+1:], 8), 8)
				if err != nil {
					return nil, nil, nil, err
				}

				builder.WriteString(x)
				i += 8
			default:
				return nil, nil, nil, newDecodeError(token[i:i+1], "invalid escaped character %#U", c)
			}
		} else {
			builder.WriteByte(c)
		}
	}

	return token, builder.Bytes(), rest, nil
}

func (p *parser) parseKey(b []byte) (ast.Reference, []byte, error) {
	// key = simple-key / dotted-key
	// simple-key = quoted-key / unquoted-key
	//
	// unquoted-key = 1*( ALPHA / DIGIT / %x2D / %x5F ) ; A-Z / a-z / 0-9 / - / _
	// quoted-key = basic-string / literal-string
	// dotted-key = simple-key 1*( dot-sep simple-key )
	//
	// dot-sep   = ws %x2E ws  ; . Period
	raw, key, b, err := p.parseSimpleKey(b)
	if err != nil {
		return ast.InvalidReference, nil, err
	}

	ref := p.builder.Push(ast.Node{
		Kind: ast.Key,
		Raw:  p.Range(raw),
		Data: key,
	})
	lastRef := ref

	for {
		var ws []byte
		ws, b = scanWhitespace(b)
		if len(b) > 0 && b[0] == '.' {
			dot := b[:1]
			var ws2 []byte
			ws2, b = scanWhitespace(b[1:])

			raw, key, b, err = p.parseSimpleKey(b)
			if err != nil {
				return ref, nil, err
			}

			reft := p.builder.Push(ast.Node{
				Kind: ast.Key,
				Raw:  p.Range(raw),
				Data: key,
			})
			p.builder.Chain(lastRef, reft)
			lastRef = reft

			if p.xast {
				sym := p.builder.Push(ast.Node{
					Kind: ast.Symbol,
					Data: dot,
				})
				p.builder.AttachChild(lastRef, sym)

				if len(ws) > 0 || len(ws2) > 0 {
					p.decm[sym] = &Decoration{
						Before: ws,
						After:  ws2,
					}
				}
			}
		} else {
			break
		}
	}

	return ref, b, nil
}

func (p *parser) parseSimpleKey(b []byte) (raw, key, rest []byte, err error) {
	// simple-key = quoted-key / unquoted-key
	// unquoted-key = 1*( ALPHA / DIGIT / %x2D / %x5F ) ; A-Z / a-z / 0-9 / - / _
	// quoted-key = basic-string / literal-string
	if len(b) == 0 {
		return nil, nil, nil, newDecodeError(b, "key is incomplete")
	}

	switch {
	case b[0] == '\'':
		return p.parseLiteralString(b)
	case b[0] == '"':
		return p.parseBasicString(b)
	case isUnquotedKeyChar(b[0]):
		key, rest = scanUnquotedKey(b)
		return key, key, rest, nil
	default:
		return nil, nil, nil, newDecodeError(b[0:1], "invalid character at start of key: %c", b[0])
	}
}

//nolint:funlen,cyclop
func (p *parser) parseBasicString(b []byte) ([]byte, []byte, []byte, error) {
	// basic-string = quotation-mark *basic-char quotation-mark
	// quotation-mark = %x22            ; "
	// basic-char = basic-unescaped / escaped
	// basic-unescaped = wschar / %x21 / %x23-5B / %x5D-7E / non-ascii
	// escaped = escape escape-seq-char
	// escape-seq-char =  %x22         ; "    quotation mark  U+0022
	// escape-seq-char =/ %x5C         ; \    reverse solidus U+005C
	// escape-seq-char =/ %x62         ; b    backspace       U+0008
	// escape-seq-char =/ %x66         ; f    form feed       U+000C
	// escape-seq-char =/ %x6E         ; n    line feed       U+000A
	// escape-seq-char =/ %x72         ; r    carriage return U+000D
	// escape-seq-char =/ %x74         ; t    tab             U+0009
	// escape-seq-char =/ %x75 4HEXDIG ; uXXXX                U+XXXX
	// escape-seq-char =/ %x55 8HEXDIG ; UXXXXXXXX            U+XXXXXXXX
	token, rest, err := scanBasicString(b)
	if err != nil {
		return nil, nil, nil, err
	}

	// fast path
	i := len(`"`)
	startIdx := i
	endIdx := len(token) - len(`"`)
	for ; i < endIdx; i++ {
		if token[i] == '\\' {
			break
		}
	}
	if i == endIdx {
		return token, token[startIdx:endIdx], rest, nil
	}

	var builder bytes.Buffer
	builder.Write(token[startIdx:i])

	// The scanner ensures that the token starts and ends with quotes and that
	// escapes are balanced.
	for ; i < len(token)-1; i++ {
		c := token[i]
		if c == '\\' {
			i++
			c = token[i]

			switch c {
			case '"', '\\':
				builder.WriteByte(c)
			case 'b':
				builder.WriteByte('\b')
			case 'f':
				builder.WriteByte('\f')
			case 'n':
				builder.WriteByte('\n')
			case 'r':
				builder.WriteByte('\r')
			case 't':
				builder.WriteByte('\t')
			case 'u':
				x, err := hexToString(token[i+1:len(token)-1], 4)
				if err != nil {
					return nil, nil, nil, err
				}

				builder.WriteString(x)
				i += 4
			case 'U':
				x, err := hexToString(token[i+1:len(token)-1], 8)
				if err != nil {
					return nil, nil, nil, err
				}

				builder.WriteString(x)
				i += 8
			default:
				return nil, nil, nil, newDecodeError(token[i:i+1], "invalid escaped character %#U", c)
			}
		} else {
			builder.WriteByte(c)
		}
	}

	return token, builder.Bytes(), rest, nil
}

func hexToString(b []byte, length int) (string, error) {
	if len(b) < length {
		return "", newDecodeError(b, "unicode point needs %d character, not %d", length, len(b))
	}
	b = b[:length]

	//nolint:godox
	// TODO: slow
	intcode, err := strconv.ParseInt(string(b), 16, 32)
	if err != nil {
		return "", newDecodeError(b, "couldn't parse hexadecimal number: %w", err)
	}

	return string(rune(intcode)), nil
}

func (p *parser) parseWhitespace(b []byte) []byte {
	// ws = *wschar
	// wschar =  %x20  ; Space
	// wschar =/ %x09  ; Horizontal tab
	_, rest := scanWhitespace(b)

	return rest
}

//nolint:cyclop
func (p *parser) parseIntOrFloatOrDateTime(b []byte) (ast.Reference, []byte, error) {
	switch b[0] {
	case 'i':
		if !scanFollowsInf(b) {
			return ast.InvalidReference, nil, newDecodeError(atmost(b, 3), "expected 'inf'")
		}

		return p.builder.Push(ast.Node{
			Kind: ast.Float,
			Data: b[:3],
		}), b[3:], nil
	case 'n':
		if !scanFollowsNan(b) {
			return ast.InvalidReference, nil, newDecodeError(atmost(b, 3), "expected 'nan'")
		}

		return p.builder.Push(ast.Node{
			Kind: ast.Float,
			Data: b[:3],
		}), b[3:], nil
	case '+', '-':
		return p.scanIntOrFloat(b)
	}

	//nolint:gomnd
	if len(b) < 3 {
		return p.scanIntOrFloat(b)
	}

	s := 5
	if len(b) < s {
		s = len(b)
	}

	for idx, c := range b[:s] {
		if isDigit(c) {
			continue
		}

		if idx == 2 && c == ':' || (idx == 4 && c == '-') {
			return p.scanDateTime(b)
		}
	}

	return p.scanIntOrFloat(b)
}

func digitsToInt(b []byte) int {
	x := 0

	for _, d := range b {
		x *= 10
		x += int(d - '0')
	}

	return x
}

//nolint:gocognit,cyclop
func (p *parser) scanDateTime(b []byte) (ast.Reference, []byte, error) {
	// scans for contiguous characters in [0-9T:Z.+-], and up to one space if
	// followed by a digit.
	hasTime := false
	hasTz := false
	seenSpace := false

	i := 0
byteLoop:
	for ; i < len(b); i++ {
		c := b[i]

		switch {
		case isDigit(c):
		case c == '-':
			const minOffsetOfTz = 8
			if i >= minOffsetOfTz {
				hasTz = true
			}
		case c == 'T' || c == ':' || c == '.':
			hasTime = true
		case c == '+' || c == '-' || c == 'Z':
			hasTz = true
		case c == ' ':
			if !seenSpace && i+1 < len(b) && isDigit(b[i+1]) {
				i += 2
				seenSpace = true
				hasTime = true
			} else {
				break byteLoop
			}
		default:
			break byteLoop
		}
	}

	var kind ast.Kind

	if hasTime {
		if hasTz {
			kind = ast.DateTime
		} else {
			kind = ast.LocalDateTime
		}
	} else {
		kind = ast.LocalDate
	}

	return p.builder.Push(ast.Node{
		Kind: kind,
		Data: b[:i],
	}), b[i:], nil
}

//nolint:funlen,gocognit,cyclop
func (p *parser) scanIntOrFloat(b []byte) (ast.Reference, []byte, error) {
	i := 0

	if len(b) > 2 && b[0] == '0' && b[1] != '.' {
		var isValidRune validRuneFn

		switch b[1] {
		case 'x':
			isValidRune = isValidHexRune
		case 'o':
			isValidRune = isValidOctalRune
		case 'b':
			isValidRune = isValidBinaryRune
		default:
			i++
		}

		if isValidRune != nil {
			i += 2
			for ; i < len(b); i++ {
				if !isValidRune(b[i]) {
					break
				}
			}
		}

		return p.builder.Push(ast.Node{
			Kind: ast.Integer,
			Data: b[:i],
		}), b[i:], nil
	}

	isFloat := false

	for ; i < len(b); i++ {
		c := b[i]

		if c >= '0' && c <= '9' || c == '+' || c == '-' || c == '_' {
			continue
		}

		if c == '.' || c == 'e' || c == 'E' {
			isFloat = true

			continue
		}

		if c == 'i' {
			if scanFollowsInf(b[i:]) {
				return p.builder.Push(ast.Node{
					Kind: ast.Float,
					Data: b[:i+3],
				}), b[i+3:], nil
			}

			return ast.InvalidReference, nil, newDecodeError(b[i:i+1], "unexpected character 'i' while scanning for a number")
		}

		if c == 'n' {
			if scanFollowsNan(b[i:]) {
				return p.builder.Push(ast.Node{
					Kind: ast.Float,
					Data: b[:i+3],
				}), b[i+3:], nil
			}

			return ast.InvalidReference, nil, newDecodeError(b[i:i+1], "unexpected character 'n' while scanning for a number")
		}

		break
	}

	if i == 0 {
		return ast.InvalidReference, b, newDecodeError(b, "incomplete number")
	}

	kind := ast.Integer

	if isFloat {
		kind = ast.Float
	}

	return p.builder.Push(ast.Node{
		Kind: kind,
		Data: b[:i],
	}), b[i:], nil
}

func isDigit(r byte) bool {
	return r >= '0' && r <= '9'
}

type validRuneFn func(r byte) bool

func isValidHexRune(r byte) bool {
	return r >= 'a' && r <= 'f' ||
		r >= 'A' && r <= 'F' ||
		r >= '0' && r <= '9' ||
		r == '_'
}

func isValidOctalRune(r byte) bool {
	return r >= '0' && r <= '7' || r == '_'
}

func isValidBinaryRune(r byte) bool {
	return r == '0' || r == '1' || r == '_'
}

func expect(x byte, b []byte) ([]byte, error) {
	if b[0] != x {
		return nil, newDecodeError(b[0:1], "expected character %U", x)
	}

	return b[1:], nil
}
