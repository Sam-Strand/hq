package mkon

import (
	"strconv"
	"strings"
)

// SyntaxError — ошибка разбора. Error() даёт тот же текст, что и mkon-js.
type SyntaxError struct {
	Msg     string // сообщение без позиции
	Line    int    // как в JS: строка с 1
	Col     int    // как в JS: колонка (в UTF-16) от начала строки
	Context string // фрагмент текста у места ошибки
	Hint    string // подсказка о вероятной причине (может быть пустой)
}

func (e *SyntaxError) Error() string {
	return e.Msg + " at line " + strconv.Itoa(e.Line) + "," + strconv.Itoa(e.Col) + " >>>" + e.Context + " ..."
}

// ParseOptions — опции разбора.
type ParseOptions struct {
	KeepComments bool // сохранять комментарии (как mkon.rt.parse в JS)
}

// Parse разбирает текст mkon.
func Parse(text string) (any, error) { return ParseWith(text, ParseOptions{}) }

// ParseWith разбирает текст mkon с опциями.
func ParseWith(text string, opt ParseOptions) (v any, err error) {
	p := &parser{t: toUnits(text), ch: ' ', keep: opt.KeepComments}
	p.n = len(p.t)
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(*SyntaxError)
			if !ok {
				panic(r)
			}
			v, err = nil, se
		}
	}()
	return p.rootValue(), nil
}

const eof = -1

type parser struct {
	t    []uint16
	n    int
	at   int
	ch   int // текущий code unit; eof — «пустая строка» JS
	keep bool
}

// ------------------------------------------------------------------ basics

func (p *parser) charAt(i int) int {
	if i >= 0 && i < p.n {
		return int(p.t[i])
	}
	return eof
}

func (p *parser) next() int {
	p.ch = p.charAt(p.at)
	p.at++
	return p.ch
}

// jump moves to the character at pos (same as calling next() until we get there).
func (p *parser) jump(pos int) {
	p.at = pos
	p.next()
}

func (p *parser) peek(offs int) int { return p.charAt(p.at + offs) }

func (p *parser) slice(a, b int) string {
	if a < 0 {
		a = 0
	}
	if b > p.n {
		b = p.n
	}
	if a >= b {
		return ""
	}
	return fromUnits(p.t[a:b])
}

func (p *parser) fail(m string) {
	col, line := 0, 1
	i := p.at - 1
	for ; i > 0 && p.charAt(i) != '\n'; i, col = i-1, col+1 {
	}
	for ; i > 0; i-- {
		if p.charAt(i) == '\n' {
			line++
		}
	}
	start := p.at - col
	panic(&SyntaxError{Msg: m, Line: line, Col: col, Context: p.slice(start, start+20)})
}

func chStr(c int) string {
	if c == eof {
		return ""
	}
	return string(utf16Rune(c))
}

func utf16Rune(c int) rune { return rune(c) }

func isPunctuator(c int) bool {
	return c == '{' || c == '}' || c == '[' || c == ']' || c == ',' || c == ':'
}

// ----------------------------------------------------------------- strings

var escapee = map[int]uint16{'"': '"', '\'': '\'', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}

func (p *parser) string(allowML bool) string {
	var s []uint16
	exitCh := p.ch
	for p.next() != eof {
		ch := p.ch
		if ch == exitCh {
			p.next()
			if allowML && exitCh == '\'' && p.ch == '\'' && len(s) == 0 {
				// ''' indicates a multiline string
				p.next()
				return p.mlString()
			}
			return fromUnits(s)
		}
		if ch == '\\' {
			p.next()
			ch = p.ch
			if ch == 'u' {
				uffff := 0
				for i := 0; i < 4; i++ {
					p.next()
					c := p.ch
					var hex int
					switch {
					case c >= '0' && c <= '9':
						hex = c - 48
					case c >= 'a' && c <= 'f':
						hex = c - 97 + 0xa
					case c >= 'A' && c <= 'F':
						hex = c - 65 + 0xa
					default:
						p.fail("Bad \\u char " + chStr(c))
					}
					uffff = uffff*16 + hex
				}
				s = append(s, uint16(uffff))
			} else if e, ok := escapee[ch]; ok {
				s = append(s, e)
			} else {
				break
			}
		} else if ch == '\n' || ch == '\r' {
			p.fail("Bad string containing newline")
		} else {
			s = append(s, uint16(ch))
		}
	}
	p.fail("Bad string")
	return ""
}

func (p *parser) mlString() string {
	// We are just past the opening '''. The common indent is the smallest indent among the
	// non-blank lines and the line of the closing ''' (when only whitespace precedes it).
	// The column of the opening ''' does not matter. \r is dropped (CRLF files).
	start, end := p.at-1, -1
	for i := start; i+2 < p.n; i++ {
		if p.t[i] == '\'' && p.t[i+1] == '\'' && p.t[i+2] == '\'' {
			end = i
			break
		}
	}
	if end < 0 {
		p.jump(p.n)
		p.fail("Bad multiline string")
	}
	p.jump(end + 3)

	isWS := func(c uint16) bool { return c <= ' ' && c != '\n' }
	lead := func(l []uint16) int {
		i := 0
		for i < len(l) && isWS(l[i]) {
			i++
		}
		return i
	}
	// split into lines, dropping \r
	var lines [][]uint16
	cur := []uint16{}
	for _, c := range p.t[start:end] {
		switch c {
		case '\r':
		case '\n':
			lines, cur = append(lines, cur), []uint16{}
		default:
			cur = append(cur, c)
		}
	}
	lines = append(lines, cur)

	// text right after the opening ''' (one-line form or a first line)
	first := lines[0][lead(lines[0]):]
	if len(lines) == 1 {
		return fromUnits(first)
	}
	lines = lines[1:]
	last := lines[len(lines)-1]
	closingAlone := lead(last) == len(last)
	base := -1
	if closingAlone {
		base = len(last)
	}
	for _, l := range lines {
		if n := lead(l); n < len(l) && (base < 0 || n < base) {
			base = n
		}
	}
	if base < 0 {
		base = 0
	}
	if closingAlone {
		lines = lines[:len(lines)-1]
	}
	var out []uint16
	if len(first) > 0 {
		out = append(out, first...)
		if len(lines) > 0 {
			out = append(out, '\n')
		}
	}
	for i, l := range lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, l[min(base, lead(l)):]...)
	}
	return fromUnits(out)
}

func (p *parser) keyname() string {
	if p.ch == '"' || p.ch == '\'' {
		return p.string(false)
	}
	var name []uint16
	start, space := p.at, -1
	for {
		ch := p.ch
		switch {
		case ch == ':':
			if len(name) == 0 {
				p.fail("Found ':' but no key name (for an empty key name use quotes)")
			} else if space >= 0 && space != len(name) {
				p.at = start + space
				p.fail("Found whitespace in your key name (use quotes to include)")
			}
			return fromUnits(name)
		case ch <= ' ': // включая eof (-1)
			if ch == eof {
				p.fail("Found EOF while looking for a key name (check your syntax)")
			} else if space < 0 {
				space = len(name)
			}
		case isPunctuator(ch):
			p.fail("Found '" + chStr(ch) + "' where a key name was expected (check your syntax or use quotes if the key name includes {}[],: or whitespace)")
		default:
			name = append(name, uint16(ch))
		}
		p.next()
	}
}

func (p *parser) white() {
	for p.ch != eof {
		for p.ch != eof && p.ch <= ' ' {
			p.next()
		}
		// mkon allows JS-style comments only: // and /* */
		if p.ch == '/' && p.peek(0) == '/' {
			for p.ch != eof && p.ch != '\n' {
				p.next()
			}
		} else if p.ch == '/' && p.peek(0) == '*' {
			p.next()
			p.next()
			for p.ch != eof && !(p.ch == '*' && p.peek(0) == '/') {
				p.next()
			}
			if p.ch != eof {
				p.next()
				p.next()
			}
		} else {
			break
		}
	}
}

// tfnns — quoteless-строка, число, NaN/±Infinity, дата, true/false/null.
func (p *parser) tfnns(inContainer bool) any {
	if isPunctuator(p.ch) {
		p.fail("Found a punctuator character '" + chStr(p.ch) + "' when expecting a quoteless string (check your syntax)")
	}
	start := p.at - 1
	depth := 0 // brackets opened inside this quoteless string
	for {
		p.next()
		ch := p.ch
		isEOL := ch == '\r' || ch == '\n' || ch == eof
		isSep := ch == ',' || ch == '}' || ch == ']'
		endsString := isEOL || (inContainer && isSep && depth == 0)
		if endsString || isSep || (ch == '/' && (p.peek(0) == '/' || p.peek(0) == '*')) {
			v := p.slice(start, p.at-1)
			chf := byte(0)
			if v != "" {
				chf = v[0]
			}
			t := jsTrim(v)
			switch {
			case chf == 'f':
				if t == "false" {
					return false
				}
			case chf == 'n':
				if t == "null" {
					return nil
				}
			case chf == 't':
				if t == "true" {
					return true
				}
			case chf == 'N' || chf == 'I' || chf == '+':
				if sp, ok := parseSpecialNumber(t); ok {
					return sp
				}
			case chf == '-' || (chf >= '0' && chf <= '9'):
				if n, ok := tryParseNumber(v, false); ok {
					return n
				}
				if chf != '-' {
					d, err := parseISODate(t)
					if err != nil {
						p.fail(err.Error())
					}
					if d != nil {
						return d
					}
				}
				if chf == '-' {
					if sn, ok := parseSpecialNumber(t); ok {
						return sn
					}
				}
			}
			if endsString {
				return t
			}
		}
		if ch == '{' || ch == '[' {
			depth++
		} else if (ch == '}' || ch == ']') && depth > 0 {
			depth--
		}
	}
}

// ---------------------------------------------------------------- comments

func (p *parser) readCommentAt(cAt int, first bool) []string {
	cAt--
	// remove trailing whitespace, but only up to EOL
	i := p.at - 2
	for i > cAt && i >= 0 && i < p.n && p.t[i] <= ' ' && p.t[i] != '\n' {
		i--
	}
	if i >= 0 && i < p.n && p.t[i] == '\r' {
		i--
	}
	res := []uint16{}
	if l := i - cAt + 1; l > 0 && cAt >= 0 {
		end := cAt + l
		if end > p.n {
			end = p.n
		}
		if cAt < end {
			res = p.t[cAt:end]
		}
	}
	for _, c := range res {
		if c > ' ' {
			for j, c2 := range res {
				if c2 == '\n' {
					c := []string{jsRTrim(fromUnits(res[:j])), jsRTrim(fromUnits(res[j+1:]))}
					if first && len(jsTrim(c[0])) == 0 {
						c = c[1:]
					}
					return c
				}
			}
			return []string{jsRTrim(fromUnits(res))}
		}
	}
	return nil
}

func errorClosingHint(value any) string {
	var search func(v any, ch string) (string, bool)
	search = func(v any, ch string) (res string, found bool) {
		switch x := v.(type) {
		case string:
			if strings.Contains(x, ch) {
				return x, true
			}
		case *Array:
			for _, it := range x.Items {
				if r, ok := search(it, ch); ok {
					res, found = r, true
				}
			}
		case *Object:
			for _, k := range jsKeyOrder(x.keys) {
				if r, ok := search(x.values[k], ch); ok {
					res, found = r, true
				}
			}
		}
		return
	}
	report := func(ch string) string {
		if s, ok := search(value, ch); ok && s != "" {
			return "found '" + ch + "' in a string value, your mistake could be with:\n" +
				"  > " + s + "\n" +
				"  (unquoted strings end at the next line or at a ',' / '}' / ']' outside of brackets!)"
		}
		return ""
	}
	if h := report("}"); h != "" {
		return h
	}
	return report("]")
}

func withHint(value any) {
	if r := recover(); r != nil {
		if se, ok := r.(*SyntaxError); ok && se.Hint == "" {
			se.Hint = errorClosingHint(value)
		}
		panic(r)
	}
}

// -------------------------------------------------------------- containers

func first(c []string) string {
	if len(c) > 0 {
		return c[0]
	}
	return ""
}

func (p *parser) array() *Array {
	arr := &Array{}
	defer withHint(arr)
	var com *Comments
	var nextComment *string
	if p.keep {
		com = &Comments{}
		arr.Comments = com
	}

	p.next()
	cAt := p.at
	p.white()
	if com != nil {
		s := strings.Join(p.readCommentAt(cAt, true), "\n")
		nextComment = &s
	}
	if p.ch == ']' {
		p.next()
		if com != nil {
			com.E, com.HasE = []string{*nextComment}, true
		}
		return arr
	}
	for p.ch != eof {
		arr.Items = append(arr.Items, p.value(true))
		cAt = p.at
		p.white()
		// the comma is optional and trailing commas are allowed
		if p.ch == ',' {
			p.next()
			cAt = p.at
			p.white()
		}
		if com != nil {
			c := p.readCommentAt(cAt, false)
			com.A = append(com.A, [2]string{deref(nextComment), first(c)})
			nextComment = second(c)
		}
		if p.ch == ']' {
			p.next()
			if com != nil {
				com.A[len(com.A)-1][1] += deref(nextComment)
			}
			return arr
		}
		p.white()
	}
	p.fail("End of input while parsing an array (missing ']')")
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func second(c []string) *string {
	if len(c) > 1 {
		return &c[1]
	}
	return nil
}

func (p *parser) object(withoutBraces bool) *Object {
	obj := NewObject()
	defer withHint(obj)
	var com *Comments
	var nextComment *string
	if p.keep {
		com = &Comments{C: map[string][2]string{}}
		obj.Comments = com
	}

	var cAt int
	if !withoutBraces {
		p.next()
		cAt = p.at
		p.white()
		if com != nil {
			s := strings.Join(p.readCommentAt(cAt, true), "\n")
			nextComment = &s
		}
	} else {
		// the leading comment of a braceless root is stored as the root comment
		p.white()
		cAt = p.at
	}

	if p.ch == '}' && !withoutBraces {
		if com != nil {
			com.E, com.HasE = []string{deref(nextComment)}, true
		}
		p.next()
		return obj
	}
	for p.ch != eof {
		key := p.keyname()
		p.white()
		if p.ch != ':' {
			p.fail("Expected ':' instead of '" + chStr(p.ch) + "'")
		}
		p.next()
		// duplicate keys overwrite the previous value (and keep its position)
		obj.Set(key, p.value(true))
		cAt = p.at
		p.white()
		if p.ch == ',' {
			p.next()
			cAt = p.at
			p.white()
		}
		if com != nil {
			c := p.readCommentAt(cAt, false)
			if _, seen := com.C[key]; !seen {
				com.O = append(com.O, key)
			}
			com.C[key] = [2]string{deref(nextComment), first(c)}
			nextComment = second(c)
		}
		if p.ch == '}' && !withoutBraces {
			p.next()
			if com != nil {
				e := com.C[key]
				e[1] += deref(nextComment)
				com.C[key] = e
			}
			return obj
		}
		p.white()
	}
	if withoutBraces {
		return obj
	}
	p.fail("End of input while parsing an object (missing '}')")
	return nil
}

func (p *parser) value(inContainer bool) any {
	p.white()
	switch p.ch {
	case '{':
		return p.object(false)
	case '[':
		return p.array()
	case '\'', '"':
		return p.string(true)
	}
	return p.tfnns(inContainer)
}

// -------------------------------------------------------------------- root

func (p *parser) checkTrailing(v any, c []string) any {
	cAt := p.at
	p.white()
	if p.ch != eof {
		p.fail("Syntax error, found trailing characters")
	}
	if p.keep {
		b := strings.Join(c, "\n")
		a := strings.Join(p.readCommentAt(cAt, false), "\n")
		if a != "" || b != "" {
			// comments can only be attached to containers and dates
			var com **Comments
			switch x := v.(type) {
			case *Object:
				com = &x.Comments
			case *Array:
				com = &x.Comments
			case *Date:
				com = &x.Comments
			}
			if com != nil {
				if *com == nil {
					*com = &Comments{}
				}
				(*com).R, (*com).HasR = [2]string{b, a}, true
			}
		}
	}
	return v
}

func (p *parser) isRootDateTime() bool {
	i := p.at - 1
	if i < 0 || i+14 > p.n {
		return false
	}
	pat := "dddd-dd-ddTdd:"
	for k := 0; k < 14; k++ {
		c := p.t[i+k]
		if pat[k] == 'd' {
			if c < '0' || c > '9' {
				return false
			}
		} else if c != uint16(pat[k]) {
			return false
		}
	}
	return true
}

// try выполняет f; при SyntaxError возвращает ok=false (как try/catch в JS).
func (p *parser) try(f func() any) (v any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isSE := r.(*SyntaxError); !isSE {
				panic(r)
			}
			ok = false
		}
	}()
	return f(), true
}

func (p *parser) rootValue() any {
	p.white()
	var c []string
	if p.keep {
		c = p.readCommentAt(1, false)
	}
	switch p.ch {
	case '{':
		return p.checkTrailing(p.object(false), c)
	case '[':
		return p.checkTrailing(p.array(), c)
	}

	at, ch := p.at, p.ch
	// A bare ISO date-time at the root ("2024-01-15T10:30Z") contains ':'
	// and would otherwise be read as a braceless object.
	if p.isRootDateTime() {
		if v, ok := p.try(func() any {
			d := p.value(false)
			if dd, isDate := d.(*Date); isDate {
				return p.checkTrailing(dd, c)
			}
			return nil
		}); ok && v != nil {
			return v
		}
		p.at, p.ch = at, ch
	}

	if v, ok := p.try(func() any { return p.checkTrailing(p.object(true), c) }); ok {
		return v
	}
	p.at, p.ch = at, ch
	return p.checkTrailing(p.value(false), c)
}
