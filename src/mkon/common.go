package mkon

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// ---------------------------------------------------------------------------
// JS-совместимые строки
// ---------------------------------------------------------------------------

// jsWS — символы класса \s в регулярках JS (и для String.prototype.trim).
const jsWS = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// jsWSClass — то же для RE2 (\s в Go — только ASCII).
const jsWSClass = `\t\n\x0b\x0c\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// jsDot — `.` в JS не матчит переводы строк.
const jsDot = `[^\n\r\x{2028}\x{2029}]`

func jsTrim(s string) string    { return strings.Trim(s, jsWS) }
func jsRTrim(s string) string   { return strings.TrimRight(s, jsWS) }
func jsLTrim(s string) string   { return strings.TrimLeft(s, jsWS) }
func toUnits(s string) []uint16 { return utf16.Encode([]rune(s)) }
func fromUnits(u []uint16) string {
	return string(utf16.Decode(u))
}

// utf16Len — длина строки, как её видит JS (str.length).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// utf16Less — сравнение строк как у Array.prototype.sort() по умолчанию.
func utf16Less(a, b string) bool {
	ua, ub := toUnits(a), toUnits(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

var arrayIndexRE = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

// jsKeyOrder — порядок ключей объекта в JS (for...in): индексы массива по возрастанию,
// затем остальные ключи в порядке вставки.
func jsKeyOrder(keys []string) []string {
	var idx, other []string
	for _, k := range keys {
		if arrayIndexRE.MatchString(k) {
			if n, err := strconv.ParseUint(k, 10, 64); err == nil && n < 4294967295 {
				idx = append(idx, k)
				continue
			}
		}
		other = append(other, k)
	}
	sort.SliceStable(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 64)
		b, _ := strconv.ParseUint(idx[j], 10, 64)
		return a < b
	})
	return append(idx, other...)
}

// ---------------------------------------------------------------------------
// Числа
// ---------------------------------------------------------------------------

// tryParseNumber — порт tryParseNumber из JS. ok=false, если текст — не число.
// ch == -1 соответствует пустой строке JS (конец текста).
func tryParseNumber(text string, stopAtNext bool) (float64, bool) {
	at := 0
	next := func() int {
		if at < len(text) {
			c := int(text[at])
			at++
			return c
		}
		at++
		return -1
	}
	var b strings.Builder
	leadingZeros := 0
	testLeading := true

	ch := next()
	if ch == '-' {
		b.WriteByte('-')
		ch = next()
	}
	for ch >= '0' && ch <= '9' {
		if testLeading {
			if ch == '0' {
				leadingZeros++
			} else {
				testLeading = false
			}
		}
		b.WriteByte(byte(ch))
		ch = next()
	}
	if testLeading {
		leadingZeros--
	}
	if ch == '.' {
		b.WriteByte('.')
		ch = next()
		for ch >= '0' && ch <= '9' {
			b.WriteByte(byte(ch))
			ch = next()
		}
	}
	if ch == 'e' || ch == 'E' {
		b.WriteByte(byte(ch))
		ch = next()
		if ch == '-' || ch == '+' {
			b.WriteByte(byte(ch))
			ch = next()
		}
		for ch >= '0' && ch <= '9' {
			b.WriteByte(byte(ch))
			ch = next()
		}
	}
	for ch >= 0 && ch <= ' ' { // while (ch && ch <= ' ')
		ch = next()
	}
	if stopAtNext {
		nx := -1
		if at < len(text) {
			nx = int(text[at])
		}
		if ch == ',' || ch == '}' || ch == ']' || (ch == '/' && (nx == '/' || nx == '*')) {
			ch = -1
		}
	}
	if ch != -1 || leadingZeros != 0 {
		return 0, false
	}
	f, ok := jsNumber(b.String())
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// jsNumber — Number(str) для строк из [-0-9.eE+] (как их собирает tryParseNumber).
func jsNumber(s string) (float64, bool) {
	t := s
	if strings.HasSuffix(t, ".") { // "1." — в JS число
		t += "0"
	}
	if strings.HasPrefix(t, ".") || strings.HasPrefix(t, "-.") {
		t = strings.Replace(t, ".", "0.", 1)
	}
	t = strings.Replace(t, ".e", ".0e", 1)
	t = strings.Replace(t, ".E", ".0E", 1)
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && ne.Err == strconv.ErrRange {
			return f, true // ±Inf или 0 — как Number()
		}
		return 0, false
	}
	return f, true
}

// specialNumbers — специальные значения float, которые mkon пишет литералами. Регистр важен.
var specialNumbers = map[string]float64{
	"NaN":       math.NaN(),
	"Infinity":  math.Inf(1),
	"+Infinity": math.Inf(1),
	"-Infinity": math.Inf(-1),
}

func parseSpecialNumber(s string) (float64, bool) {
	v, ok := specialNumbers[s]
	return v, ok
}

// FormatNumber пишет число как Number.prototype.toString в JS (и NaN/±Infinity/-0 как в mkon).
func FormatNumber(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		if math.Signbit(x) {
			return "-0"
		}
		return "0"
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	// кратчайшие цифры: d.ddddde±XX
	e := strconv.FormatFloat(x, 'e', -1, 64)
	mant, expS, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expS)
	digits := strings.Replace(mant, ".", "", 1)
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	k := len(digits)
	n := exp + 1 // x = 0.digits × 10^n
	var s string
	switch {
	case k <= n && n <= 21:
		s = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		s = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		s = "0." + strings.Repeat("0", -n) + digits
	default:
		ee := n - 1
		es := "+"
		if ee < 0 {
			es = "-"
			ee = -ee
		}
		if k == 1 {
			s = digits + "e" + es + strconv.Itoa(ee)
		} else {
			s = digits[:1] + "." + digits[1:] + "e" + es + strconv.Itoa(ee)
		}
	}
	return sign + s
}

// ---------------------------------------------------------------------------
// ISO 8601 даты
// ---------------------------------------------------------------------------

const isoDateSrc = `([0-9]{4})-([0-9]{2})-([0-9]{2})`
const isoTimeSrc = `T([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:\.([0-9]+))?)?(Z|[+-][0-9]{2}:[0-9]{2})`

var isoRE = regexp.MustCompile(`^` + isoDateSrc + `(?:` + isoTimeSrc + `)?$`)
var isoLikeRE = regexp.MustCompile(`^` + isoDateSrc + `(?:` + isoTimeSrc + `)?[` + jsWSClass + `]*((,|\]|\}|//|/\*)` + jsDot + `*)?$`)

var daysIn = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// parseISODate — порт parseIsoDate: (nil, nil), если текст не похож на ISO-дату;
// ошибка — если похож, но дата невалидна (2024-02-30, 25:00).
func parseISODate(text string) (*Date, error) {
	m := isoRE.FindStringSubmatch(text)
	if m == nil {
		return nil, nil
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	h, mi, s, ms := 0, 0, 0, 0
	if m[4] != "" {
		h, mi = atoi(m[4]), atoi(m[5])
	}
	if m[6] != "" {
		s = atoi(m[6])
	}
	if m[7] != "" {
		ms = atoi((m[7] + "00")[:3])
	}
	leap := (y%4 == 0 && y%100 != 0) || y%400 == 0
	dim := 0
	if mo >= 1 && mo <= 12 {
		dim = daysIn[mo-1]
		if mo == 2 && leap {
			dim = 29
		}
	}
	if mo < 1 || mo > 12 || d < 1 || d > dim || h > 23 || mi > 59 || s > 59 {
		return nil, errors.New("Invalid date '" + text + "'")
	}
	offset := 0
	if tz := m[8]; tz != "" && tz != "Z" {
		oh, om := atoi(tz[1:3]), atoi(tz[4:6])
		if oh > 23 || om > 59 {
			return nil, errors.New("Invalid time zone offset in '" + text + "'")
		}
		offset = oh*60 + om
		if tz[0] == '-' {
			offset = -offset
		}
	}
	t := time.Date(y, time.Month(mo), d, h, mi, s, ms*int(time.Millisecond), time.UTC)
	t = t.Add(-time.Duration(offset) * time.Minute)
	return &Date{Time: t}, nil
}

func looksLikeISODate(s string) bool { return isoLikeRE.MatchString(s) }

// FormatDate — dateToString: YYYY-MM-DD для полуночи UTC, иначе YYYY-MM-DDTHH:MM:SS[.mmm]Z.
func FormatDate(d *Date) (string, error) {
	t := d.Time.UTC()
	y := t.Year()
	if y < 0 || y > 9999 {
		return "", errors.New("Cannot stringify date outside of years 0000-9999: " + t.Format(time.RFC3339))
	}
	ms := t.Nanosecond() / int(time.Millisecond)
	head := pad(y, 4) + "-" + pad(int(t.Month()), 2) + "-" + pad(t.Day(), 2)
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && ms == 0 {
		return head, nil
	}
	s := head + "T" + pad(t.Hour(), 2) + ":" + pad(t.Minute(), 2) + ":" + pad(t.Second(), 2)
	if ms != 0 {
		return s + "." + pad(ms, 3) + "Z", nil
	}
	return s + "Z", nil
}

func pad(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

// ---------------------------------------------------------------------------
// Комментарии
// ---------------------------------------------------------------------------

// forceComment — строкам, которые ещё не комментарии, добавляет `// `.
func forceComment(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for j := 0; j < len(lines); j++ {
		line := lines[j]
		for i := 0; i < len(line); i++ {
			c := line[i]
			if c == '/' && i+1 < len(line) && (line[i+1] == '/' || line[i+1] == '*') {
				if line[i+1] == '*' {
					j = len(lines) // блочный комментарий занимает весь остаток текста
				}
				break
			}
			if c > ' ' {
				lines[j] = "// " + line
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
