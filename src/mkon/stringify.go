package mkon

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Options — опции Stringify (те же, что у mkon-js). Начинайте с DefaultOptions().
type Options struct {
	EOL            string // "\n" или "\r\n"
	Indent         string // строка отступа
	KeepComments   bool   // писать комментарии из ParseWith(KeepComments: true)
	BracesSameLine bool   // { / [ на строке с ключом
	RootBraces     bool   // фигурные скобки вокруг корневого объекта
	Quotes         string // "min" | "keys" | "strings" | "all"
	QuoteChar      byte   // '"' или '\''
	Condense       int    // уместить объект/массив в строку, если короче (0 — выключено)
	Multiline      string // "std" | "no-tabs" | "off"
	Separator      bool   // запятые между элементами
	SortKeys       bool   // сортировать ключи
	Colors         bool   // ANSI-раскраска
}

// DefaultOptions — значения по умолчанию, как в mkon-js.
func DefaultOptions() Options {
	return Options{EOL: "\n", Indent: "  ", RootBraces: true, Quotes: "min", QuoteChar: '"', Multiline: "std"}
}

// Stringify сериализует значение с опциями по умолчанию.
func Stringify(v any) (string, error) { return StringifyWith(v, DefaultOptions()) }

// StringifyWith сериализует значение в текст mkon.
func StringifyWith(v any, o Options) (out string, err error) {
	s, err := newStringifier(o)
	if err != nil {
		return "", err
	}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(stringifyError); ok {
				out, err = "", e.error
				return
			}
			panic(r)
		}
	}()
	return s.run(v), nil
}

type stringifyError struct{ error }

func throw(format string, a ...any) { panic(stringifyError{fmt.Errorf(format, a...)}) }

// ------------------------------------------------------------------ tokens

type tok struct {
	l, r       string
	lLen, rLen int // «видимая» длина без ANSI
}

var plainTok = map[string][2]string{
	"obj": {"{", "}"}, "arr": {"[", "]"}, "key": {"", ""}, "qkey": {`"`, `"`}, "col": {":", ""},
	"com": {",", ""}, "str": {"", ""}, "qstr": {`"`, `"`}, "mstr": {"'''", "'''"}, "num": {"", ""},
	"lit": {"", ""}, "date": {"", ""}, "esc": {`\`, ""}, "uni": {`\u`, ""}, "rem": {"", ""},
}

var colorTok = map[string][2]string{
	"obj": {"\x1b[37m{\x1b[0m", "\x1b[37m}\x1b[0m"}, "arr": {"\x1b[37m[\x1b[0m", "\x1b[37m]\x1b[0m"},
	"key": {"\x1b[33m", "\x1b[0m"}, "qkey": {"\x1b[33m\"", "\"\x1b[0m"}, "col": {"\x1b[37m:\x1b[0m", ""},
	"com": {"\x1b[37m,\x1b[0m", ""}, "str": {"\x1b[37;1m", "\x1b[0m"}, "qstr": {"\x1b[37;1m\"", "\"\x1b[0m"},
	"mstr": {"\x1b[37;1m'''", "'''\x1b[0m"}, "num": {"\x1b[36;1m", "\x1b[0m"}, "lit": {"\x1b[36m", "\x1b[0m"},
	"date": {"\x1b[36;1m", "\x1b[0m"}, "esc": {"\x1b[31m\\", "\x1b[0m"}, "uni": {"\x1b[31m\\u", "\x1b[0m"},
	"rem": {"\x1b[35m", "\x1b[0m"},
}

// ----------------------------------------------------------------- regexes

const commonRange = `\x{7f}-\x{9f}\x{ad}\x{600}-\x{604}\x{70f}\x{17b4}\x{17b5}\x{200c}-\x{200f}\x{2028}-\x{202f}\x{2060}-\x{206f}\x{feff}\x{fff0}-\x{ffff}`
const ws = `[` + jsWSClass + `]`

var needsQuotes = regexp.MustCompile(`^` + ws + `|^"|^'|^/\*|^//|^\{|^\}|^\[|^\]|^:|^,|` + ws + `$|[\x00-\x1f` + commonRange + `]`)
var startsWithKeyword = regexp.MustCompile(`^(true|false|null|NaN|[+-]?Infinity)` + ws + `*((,|\]|\}|//|/\*)` + jsDot + `*)?$`)
var needsEscapeName = regexp.MustCompile(`[,\{\[\}\]` + jsWSClass + `:"']|//|/\*`)
var needsEscapeDQ = regexp.MustCompile(`[\\"\x00-\x1f` + commonRange + `]`)
var needsEscapeSQ = regexp.MustCompile(`[\\'\x00-\x1f` + commonRange + `]`)

// \r is excluded on purpose: the reader drops \r inside ”' (CRLF files)
var needsEscapeMLStd = regexp.MustCompile(`'''|^` + ws + `+$|[\x00-\x08\x0b-\x1f` + commonRange + `]`)
var needsEscapeMLNoTabs = regexp.MustCompile(`'''|^` + ws + `+$|[\x00-\x09\x0b-\x1f` + commonRange + `]`)

var meta = map[rune]string{'\b': "b", '\t': "t", '\n': "n", '\f': "f", '\r': "r", '"': `"`, '\'': "'", '\\': `\`}

// ------------------------------------------------------------- stringifier

type stringifier struct {
	o            Options
	tok          map[string]tok
	needsEscape  *regexp.Regexp
	needsEscML   *regexp.Regexp
	quoteKeys    bool
	quoteStrings bool
	multiline    int // 0 off, 1 std, 2 no-tabs
	separator    string
	gap          string
	wrapLen      int
	markers      map[uintptr]bool
}

func newStringifier(o Options) (*stringifier, error) {
	if o.EOL != "\n" && o.EOL != "\r\n" {
		return nil, errors.New(`mkon: EOL must be "\n" or "\r\n"`)
	}
	ml := map[string]int{"std": 1, "no-tabs": 2, "off": 0}
	multiline, ok := ml[o.Multiline]
	if !ok {
		return nil, errors.New(`mkon: Multiline must be "std", "no-tabs" or "off"`)
	}
	switch o.Quotes {
	case "min", "keys", "strings", "all":
	default:
		return nil, errors.New(`mkon: Quotes must be "min", "keys", "strings" or "all"`)
	}
	if o.QuoteChar != '"' && o.QuoteChar != '\'' {
		return nil, errors.New(`mkon: QuoteChar must be '"' or '\''`)
	}
	if o.Condense < 0 {
		return nil, errors.New("mkon: Condense must be >= 0")
	}
	s := &stringifier{o: o, markers: map[uintptr]bool{}}
	src := plainTok
	if o.Colors {
		src = colorTok
	}
	s.tok = map[string]tok{}
	for k, v := range src {
		l, r := v[0], v[1]
		if o.QuoteChar != '"' && (k == "qkey" || k == "qstr") {
			l = strings.ReplaceAll(l, `"`, string(o.QuoteChar))
			r = strings.ReplaceAll(r, `"`, string(o.QuoteChar))
		}
		s.tok[k] = tok{l, r, len(plainTok[k][0]), len(plainTok[k][1])}
	}
	if o.QuoteChar == '"' {
		s.needsEscape = needsEscapeDQ
	} else {
		s.needsEscape = needsEscapeSQ
	}
	if multiline == 2 {
		s.needsEscML = needsEscapeMLNoTabs
	} else {
		s.needsEscML = needsEscapeMLStd
	}
	s.quoteStrings = o.Quotes == "all" || o.Quotes == "strings"
	s.quoteKeys = o.Quotes == "all" || o.Quotes == "keys"
	if s.quoteStrings {
		multiline = 0
	}
	s.multiline = multiline
	if o.Separator {
		s.separator = ","
	}
	return s, nil
}

func (s *stringifier) wrap(t tok, v string) string {
	s.wrapLen += len(t.l) + len(t.r) - t.lLen - t.rLen
	return t.l + v + t.r
}

func (s *stringifier) quoteReplace(str string) string {
	return s.needsEscape.ReplaceAllStringFunc(str, func(a string) string {
		r := []rune(a)[0]
		if c, ok := meta[r]; ok {
			return s.wrap(s.tok["esc"], c)
		}
		return s.wrap(s.tok["uni"], fmt.Sprintf("%04x", r))
	})
}

// breaksQuoteless — обрежет ли парсер эту строку, если записать её без кавычек?
func breaksQuoteless(str string) bool {
	depth := 0
	for i := 0; i < len(str); i++ {
		switch str[i] {
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return true
			}
			depth--
		case ',':
			if depth == 0 {
				return true
			}
		}
	}
	return depth > 0 // an unclosed bracket would swallow the rest of the line
}

func startsWithNL(str string) bool {
	if str == "" {
		return false
	}
	i := 0
	if str[0] == '\r' {
		i = 1
	}
	return len(str) > i && str[i] == '\n'
}

func commentOnThisLine(str string) bool { return str != "" && !startsWithNL(str) }

// ---------------------------------------------------------------- comments

func reindent(text, level string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if st := jsLTrim(l); st != "" {
			lines[i] = level + st
		} else {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func (s *stringifier) renderComment(c, level string) string {
	if c == "" {
		return ""
	}
	return s.wrap(s.tok["rem"], reindent(forceComment(c), level))
}

func (s *stringifier) renderInlineComment(c string) string {
	if c == "" {
		return ""
	}
	return " " + s.wrap(s.tok["rem"], jsLTrim(forceComment(c)))
}

func (s *stringifier) beforeComment(c *[2]string, level string) string {
	if c != nil && c[0] != "" {
		if bc := s.renderComment(c[0], level); bc != "" {
			return s.o.EOL + bc + s.o.EOL + s.gap
		}
	}
	return s.o.EOL + s.gap
}

func (s *stringifier) afterComment(c *[2]string, ca bool, level string) string {
	if c == nil || c[1] == "" {
		return ""
	}
	if ca {
		return s.renderInlineComment(c[1])
	}
	return s.o.EOL + s.renderComment(c[1], level)
}

// ----------------------------------------------------------- strings, keys

func (s *stringifier) quote(str string, hasComment, isRoot, noIndent bool) string {
	if str == "" {
		return s.wrap(s.tok["qstr"], "")
	}
	_, isNum := tryParseNumber(str, true)
	if s.quoteStrings || hasComment || needsQuotes.MatchString(str) || breaksQuoteless(str) || isNum ||
		startsWithKeyword.MatchString(str) || looksLikeISODate(str) || (isRoot && strings.Contains(str, ":")) {
		if !s.needsEscape.MatchString(str) {
			return s.wrap(s.tok["qstr"], str)
		}
		if !s.needsEscML.MatchString(str) && !isRoot && s.multiline != 0 {
			return s.mlString(str, s.gap, s.o.BracesSameLine || noIndent, noIndent)
		}
		return s.wrap(s.tok["qstr"], s.quoteReplace(str))
	}
	return s.wrap(s.tok["str"], str)
}

func (s *stringifier) mlString(str, gap string, sameLine, shallow bool) string {
	a := strings.Split(strings.ReplaceAll(str, "\r", ""), "\n")
	if !shallow {
		gap += s.o.Indent
	}
	// one-line '''x''' form: the reader skips whitespace right after ''',
	// so a value starting with whitespace needs the block form
	if len(a) == 1 && !strings.HasPrefix(a[0], "'") && !strings.HasSuffix(a[0], "'") && a[0] != "" && a[0][0] > ' ' {
		return s.wrap(s.tok["mstr"], a[0])
	}
	// The reader takes the block indent from the column of the opening '''.
	// For an object value (not shallow) that column must equal gap, so ''' goes on its own line.
	res := s.tok["mstr"].l
	if !(sameLine && shallow) {
		res = s.o.EOL + gap + res
	}
	for _, line := range a {
		res += s.o.EOL
		if line != "" {
			res += gap + line
		}
	}
	return res + s.o.EOL + gap + s.tok["mstr"].r
}

func (s *stringifier) quoteKey(name string) string {
	if name == "" {
		return s.tok["qkey"].l + s.tok["qkey"].r
	}
	if s.quoteKeys || needsEscapeName.MatchString(name) {
		if s.needsEscape.MatchString(name) {
			return s.wrap(s.tok["qkey"], s.quoteReplace(name))
		}
		return s.wrap(s.tok["qkey"], name)
	}
	return s.wrap(s.tok["key"], name)
}

// ---------------------------------------------------------------- dispatch

// normalize приводит Go-значения к модели mkon.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, string, bool, float64, *Object, *Array, *Date:
		return v
	case time.Time:
		return NewDate(x)
	case Date:
		return &x
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case []any:
		return &Array{Items: x}
	case map[string]any:
		o := NewObject()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // у map нет порядка — берём детерминированный
		for _, k := range keys {
			o.Set(k, x[k])
		}
		return o
	}
	throw("mkon: cannot stringify value of type %T", v)
	return nil
}

func isObjectKind(v any) bool { // typeof === 'object' && v !== null
	switch v.(type) {
	case *Object, *Array, *Date:
		return true
	}
	return false
}

func (s *stringifier) str(v any, hasComment, noIndent, isRoot bool) string {
	switch x := normalize(v).(type) {
	case *Date:
		d, err := FormatDate(x)
		if err != nil {
			throw("%s", err.Error())
		}
		return s.wrap(s.tok["date"], d)
	case string:
		return s.quote(x, hasComment, isRoot, noIndent)
	case float64:
		return s.wrap(s.tok["num"], FormatNumber(x))
	case bool:
		if x {
			return s.wrap(s.tok["lit"], "true")
		}
		return s.wrap(s.tok["lit"], "false")
	case nil:
		return s.wrap(s.tok["lit"], "null")
	case *Object:
		s.mark(x)
		defer s.unmark(x)
		var c *Comments
		if s.o.KeepComments {
			c = x.Comments
		}
		return s.strObject(x, c, noIndent)
	case *Array:
		s.mark(x)
		defer s.unmark(x)
		var c *Comments
		if s.o.KeepComments {
			c = x.Comments
		}
		return s.strArray(x, c, noIndent)
	}
	return ""
}

func (s *stringifier) mark(p any) {
	id := reflect.ValueOf(p).Pointer()
	if s.markers[id] {
		throw("mkon: circular reference detected")
	}
	s.markers[id] = true
}

func (s *stringifier) unmark(p any) { delete(s.markers, reflect.ValueOf(p).Pointer()) }

type ctx struct {
	mind, eolMind, prefix string
	partial, cpartial     []string
	condense, cdisabled   bool
	saveMLMode            int
	iseparator            string
	cwrapLen              int
}

func (s *stringifier) open(noIndent bool) *ctx {
	c := &ctx{mind: s.gap}
	s.gap += s.o.Indent
	c.eolMind = s.o.EOL + c.mind
	if !(noIndent || s.o.BracesSameLine) {
		c.prefix = c.eolMind
	}
	c.condense = s.o.Condense != 0
	c.saveMLMode = s.multiline
	if s.o.Separator {
		c.iseparator = ""
	} else {
		c.iseparator = s.tok["com"].l
	}
	return c
}

func (s *stringifier) condensedString(v string, c *ctx, noIndent bool) string {
	s.wrapLen = 0
	s.multiline = 0 // ''' не помещается в строку; кавычки — по обычным правилам
	defer func() { s.multiline = c.saveMLMode }()
	return s.str(v, false, noIndent, false)
}

func (s *stringifier) finish(c *ctx, t tok, count int, com *Comments) string {
	if count == 0 {
		if com != nil && com.HasE {
			e := ""
			if len(com.E) > 0 {
				e = com.E[0]
			}
			if ec := s.renderComment(e, c.mind); ec != "" {
				c.partial = append(c.partial, c.eolMind+ec+c.eolMind)
			} else {
				c.partial = append(c.partial, c.eolMind)
			}
		}
	} else {
		c.partial = append(c.partial, c.eolMind)
	}
	var res string
	if len(c.partial) == 0 {
		res = s.wrap(t, "")
	} else {
		res = c.prefix + s.wrap(t, strings.Join(c.partial, ""))
		if c.condense && !c.cdisabled {
			cres := strings.Join(c.cpartial, " ")
			if utf16Len(cres)-c.cwrapLen <= s.o.Condense {
				res = s.wrap(t, cres)
			}
		}
	}
	s.gap = c.mind
	return res
}

func (s *stringifier) strArray(a *Array, com *Comments, noIndent bool) string {
	c := s.open(noIndent)
	n := len(a.Items)
	for i, v := range a.Items {
		setsep := i < n-1
		var cm *[2]string
		ca := false
		if com != nil {
			e := [2]string{}
			if i < len(com.A) {
				e = com.A[i]
			}
			cm = &e
			ca = commentOnThisLine(e[1])
		}
		if com != nil {
			c.partial = append(c.partial, s.beforeComment(cm, s.gap))
			if c.condense && (cm[0] != "" || cm[1] != "" || ca) {
				c.cpartial = nil
				c.cdisabled = true
			}
		} else {
			c.partial = append(c.partial, s.o.EOL+s.gap)
		}

		s.wrapLen = 0
		sep := ""
		if setsep {
			sep = s.separator
		}
		c.partial = append(c.partial, s.str(v, com != nil && ca, true, false)+sep)

		if c.condense && !c.cdisabled {
			nv := normalize(v)
			if str, ok := nv.(string); ok {
				cs := ""
				if setsep {
					cs = s.tok["com"].l
				}
				c.cpartial = append(c.cpartial, s.condensedString(str, c, true)+cs)
			} else if isObjectKind(nv) {
				c.cdisabled = true
			} else {
				is := ""
				if setsep {
					is = c.iseparator
				}
				c.cpartial = append(c.cpartial, c.partial[len(c.partial)-1]+is)
			}
			if setsep {
				s.wrapLen += len(s.tok["com"].l) - s.tok["com"].lLen
			}
			c.cwrapLen += s.wrapLen
		}

		if ac := s.afterComment(cm, ca, s.gap); ac != "" {
			c.partial = append(c.partial, ac)
		}
	}
	return s.finish(c, s.tok["arr"], n, com)
}

func (s *stringifier) objectKeys(o *Object, com *Comments) []string {
	var commentKeys []string
	inC := map[string]bool{}
	if com != nil {
		for _, k := range com.O {
			if o.Has(k) { // keys deleted from the object after parsing are skipped
				commentKeys = append(commentKeys, k)
			}
			inC[k] = true
		}
	}
	var objectKeys []string
	for _, k := range jsKeyOrder(o.keys) {
		if !inC[k] {
			objectKeys = append(objectKeys, k)
		}
	}
	if s.o.SortKeys {
		sort.SliceStable(objectKeys, func(i, j int) bool { return utf16Less(objectKeys[i], objectKeys[j]) })
	}
	return append(commentKeys, objectKeys...)
}

func (s *stringifier) strObject(o *Object, com *Comments, noIndent bool) string {
	c := s.open(noIndent)
	keys := s.objectKeys(o, com)
	n := len(keys)
	for i, k := range keys {
		setsep := i < n-1
		var cm *[2]string
		ca := false
		if com != nil {
			e := com.C[k]
			cm = &e
			ca = commentOnThisLine(e[1])
		}
		if com != nil {
			c.partial = append(c.partial, s.beforeComment(cm, s.gap))
			if c.condense && (cm[0] != "" || cm[1] != "" || ca) {
				c.cdisabled = true
			}
		} else {
			c.partial = append(c.partial, s.o.EOL+s.gap)
		}

		s.wrapLen = 0
		v := o.values[k]
		vs := s.str(v, com != nil && ca, false, false)
		sp := " "
		if startsWithNL(vs) {
			sp = ""
		}
		sep := ""
		if setsep {
			sep = s.separator
		}
		c.partial = append(c.partial, s.quoteKey(k)+s.tok["col"].l+sp+vs+sep)

		if ac := s.afterComment(cm, ca, s.gap); ac != "" {
			c.partial = append(c.partial, ac)
		}

		if c.condense && !c.cdisabled {
			nv := normalize(v)
			if str, ok := nv.(string); ok {
				cs := ""
				if setsep {
					cs = s.tok["com"].l
				}
				vs2 := s.condensedString(str, c, false)
				c.cpartial = append(c.cpartial, s.quoteKey(k)+s.tok["col"].l+" "+vs2+cs)
			} else if isObjectKind(nv) {
				c.cdisabled = true
			} else {
				is := ""
				if setsep {
					is = c.iseparator
				}
				c.cpartial = append(c.cpartial, c.partial[len(c.partial)-1]+is)
			}
			s.wrapLen += len(s.tok["col"].l) - s.tok["col"].lLen
			if setsep {
				s.wrapLen += len(s.tok["com"].l) - s.tok["com"].lLen
			}
			c.cwrapLen += s.wrapLen
		}
	}
	return s.finish(c, s.tok["obj"], n, com)
}

// -------------------------------------------------------------------- root

func (s *stringifier) run(data any) string {
	data = normalize(data)
	var root *Comments
	if s.o.KeepComments {
		switch x := data.(type) {
		case *Object:
			root = x.Comments
		case *Array:
			root = x.Comments
		case *Date:
			root = x.Comments
		}
	}
	res := s.str(data, false, true, true)
	if _, isObj := data.(*Object); isObj && !s.o.RootBraces && strings.HasPrefix(res, "{") && strings.HasSuffix(res, "}") {
		res = strings.ReplaceAll(res[1:len(res)-1], "\n"+s.o.Indent, "\n")
		res = strings.TrimPrefix(res, "\n")
		res = strings.TrimSuffix(res, "\n")
	}
	if root != nil && root.HasR {
		if root.R[0] != "" {
			res = jsRTrim(root.R[0]) + s.o.EOL + res
		}
		if root.R[1] != "" {
			res += jsRTrim(root.R[1])
		}
	}
	if !strings.HasSuffix(res, s.o.EOL) {
		res += s.o.EOL
	}
	return res
}
