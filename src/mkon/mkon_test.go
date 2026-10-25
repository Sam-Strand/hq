package mkon

// Общие тесты mkon (эталон — mkon-js): фикстуры test/assets, test/conformance/cases.json
// и кросс-проверка с живой JS-реализацией (нужны node и mkon-js; путь — $MKON_JS или рядом).

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------------------ suite

func findMkonJS() string {
	var cands []string
	if p := os.Getenv("MKON_JS"); p != "" {
		cands = append(cands, p)
	}
	wd, _ := os.Getwd() // .../mkon-sh/src/mkon
	cands = append(cands,
		filepath.Join(wd, "..", "..", "..", "mkon-js"),
		filepath.Join(wd, "..", "..", "..", "..", "graph-flow", "frontend", "git", "mkon-js"),
	)
	for _, c := range cands {
		if st, err := os.Stat(filepath.Join(c, "test", "conformance", "cases.json")); err == nil && !st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func suite(t *testing.T) string {
	root := findMkonJS()
	if root == "" {
		t.Skip("mkon-js not found (set MKON_JS) — shared suite unavailable")
	}
	return root
}

// ------------------------------------------------------------------ tagged JSON

// tag — значение mkon в «tagged JSON» в виде generic-структуры (для сравнения).
func tag(v any) any {
	switch x := normalize(v).(type) {
	case float64:
		switch {
		case math.IsNaN(x):
			return map[string]any{"$number": "NaN"}
		case math.IsInf(x, 1):
			return map[string]any{"$number": "Infinity"}
		case math.IsInf(x, -1):
			return map[string]any{"$number": "-Infinity"}
		case x == 0 && math.Signbit(x):
			return map[string]any{"$number": "-0"}
		}
		return x
	case *Date:
		return map[string]any{"$date": isoString(x.Time)}
	case *Array:
		out := []any{}
		for _, it := range x.Items {
			out = append(out, tag(it))
		}
		return out
	case *Object:
		out := map[string]any{}
		for _, k := range x.keys {
			out[k] = tag(x.values[k])
		}
		return out
	default:
		return x
	}
}

func isoString(t time.Time) string { // Date.prototype.toISOString
	t = t.UTC()
	return pad(t.Year(), 4) + "-" + pad(int(t.Month()), 2) + "-" + pad(t.Day(), 2) + "T" + pad(t.Hour(), 2) + ":" +
		pad(t.Minute(), 2) + ":" + pad(t.Second(), 2) + "." + pad(t.Nanosecond()/1e6, 3) + "Z"
}

// writeTagged — tagged JSON с сохранением порядка ключей (для запросов к JS).
func writeTagged(b *bytes.Buffer, v any) {
	switch x := normalize(v).(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || (x == 0 && math.Signbit(x)) {
			b.WriteString(`{"$number":"` + FormatNumber(x) + `"}`)
		} else {
			b.WriteString(FormatNumber(x))
		}
	case *Date:
		b.WriteString(`{"$date":"` + isoString(x.Time) + `"}`)
	case *Array:
		b.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			writeTagged(b, it)
		}
		b.WriteByte(']')
	case *Object:
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			writeTagged(b, x.values[k])
		}
		b.WriteByte('}')
	case string:
		writeJSONString(b, x)
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("null")
	}
}

// untag — tagged JSON (уже разобранный FromJSON, с порядком ключей) → модель mkon.
func untag(v any) any {
	switch x := v.(type) {
	case *Array:
		for i := range x.Items {
			x.Items[i] = untag(x.Items[i])
		}
		return x
	case *Object:
		if x.Len() == 1 {
			if s, ok := x.values["$number"].(string); ok {
				f, _ := jsNumber(map[string]string{"NaN": "NaN", "Infinity": "+Inf", "-Infinity": "-Inf", "-0": "-0"}[s])
				if s == "NaN" {
					f = math.NaN()
				}
				return f
			}
			if s, ok := x.values["$date"].(string); ok {
				t, err := time.Parse("2006-01-02T15:04:05.000Z", s)
				if err != nil {
					panic(err)
				}
				return &Date{Time: t}
			}
		}
		for _, k := range x.keys {
			x.values[k] = untag(x.values[k])
		}
		return x
	}
	return v
}

func generic(raw json.RawMessage) any {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		panic(err)
	}
	return v
}

// jsOptions — опции в формате mkon-js → Options.
func jsOptions(m map[string]any) Options {
	o := DefaultOptions()
	for k, v := range m {
		switch k {
		case "space":
			switch s := v.(type) {
			case float64:
				o.Indent = strings.Repeat(" ", int(s))
			case string:
				o.Indent = s
			}
		case "sortProps":
			o.SortKeys = v == true
		case "quotes":
			o.Quotes = v.(string)
		case "quoteChar":
			o.QuoteChar = v.(string)[0]
		case "separator":
			o.Separator = v == true
		case "bracesSameLine":
			o.BracesSameLine = v == true
		case "emitRootBraces":
			o.RootBraces = v != false
		case "condense":
			o.Condense = int(v.(float64))
		case "multiline":
			o.Multiline = v.(string)
		case "eol":
			o.EOL = v.(string)
		case "keepWsc":
			o.KeepComments = v == true
		case "colors":
			o.Colors = v == true
		}
	}
	return o
}

func objToMap(v any) map[string]any {
	m := map[string]any{}
	if o, ok := v.(*Object); ok {
		for _, k := range o.keys {
			m[k] = generic(mustTaggedJSON(o.values[k]))
		}
	}
	return m
}

func mustTaggedJSON(v any) json.RawMessage {
	var b bytes.Buffer
	writeTagged(&b, v)
	return b.Bytes()
}

// ------------------------------------------------------------------ fixtures

func TestFixtures(t *testing.T) {
	root := suite(t)
	assets := filepath.Join(root, "test", "assets")
	list, err := os.ReadFile(filepath.Join(assets, "testlist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	load := func(name string) string {
		b, err := os.ReadFile(filepath.Join(assets, name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), "\r", "")
	}
	for _, file := range strings.Split(string(list), "\n") {
		if !strings.Contains(file, "_test.") {
			continue
		}
		name := strings.SplitN(file, "_test.", 2)[0]
		t.Run(file, func(t *testing.T) {
			text := load(file)
			base := filepath.Base(name)
			if strings.HasPrefix(base, "fail") {
				if _, err := Parse(text); err == nil {
					t.Fatal("expected a parse error")
				}
				return
			}
			v, err := Parse(text)
			if err != nil {
				t.Fatal(err)
			}
			// JSON не умеет -0: сравниваем как JSON
			want := generic(json.RawMessage(load(name + "_result.json")))
			got, _ := ToJSON(v, "")
			if !reflect.DeepEqual(generic(got), want) {
				t.Fatalf("data differs:\n got %s", got)
			}
			opts := DefaultOptions()
			if meta, err := os.ReadFile(filepath.Join(assets, name+"_testmeta.mkon")); err == nil {
				m, err := Parse(string(meta))
				if err != nil {
					t.Fatal(err)
				}
				if mo, ok := m.(*Object); ok {
					if o, ok := mo.values["options"]; ok {
						opts = jsOptions(objToMap(o))
					}
				}
			}
			out, err := StringifyWith(v, opts)
			if err != nil {
				t.Fatal(err)
			}
			if exp := load(name + "_result.mkon"); out != exp {
				t.Fatalf("stringify differs:\n got %q\nwant %q", out, exp)
			}
		})
	}
}

// ------------------------------------------------------------------ conformance

type conformanceCase struct {
	Name    string          `json:"name"`
	Mkon    *string         `json:"mkon"`
	Input   json.RawMessage `json:"input"`
	Options map[string]any  `json:"options"`
	RT      bool            `json:"rt"`
	Value   json.RawMessage `json:"value"`
	Text    string          `json:"text"`
	Error   bool            `json:"error"`
	Message string          `json:"message"`
}

func TestConformance(t *testing.T) {
	root := suite(t)
	data, err := os.ReadFile(filepath.Join(root, "test", "conformance", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []conformanceCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			opts := jsOptions(c.Options)
			opts.KeepComments = opts.KeepComments || c.RT
			parse := func(s string) (any, error) { return ParseWith(s, ParseOptions{KeepComments: c.RT}) }
			var input any
			if c.Mkon == nil {
				iv, err := FromJSON(c.Input)
				if err != nil {
					t.Fatal(err)
				}
				input = untag(iv)
			}
			if c.Error {
				var err error
				if c.Mkon != nil {
					_, err = parse(*c.Mkon)
				} else {
					_, err = StringifyWith(input, opts)
				}
				if err == nil || !strings.Contains(err.Error(), c.Message) {
					t.Fatalf("expected error %q, got %v", c.Message, err)
				}
				return
			}
			want := generic(c.Value)
			var out string
			if c.Mkon != nil {
				v, err := parse(*c.Mkon)
				if err != nil {
					t.Fatal(err)
				}
				if got := generic(mustTaggedJSON(v)); !reflect.DeepEqual(got, want) {
					t.Fatalf("parse differs:\n got %v\nwant %v", got, want)
				}
				if out, err = StringifyWith(v, opts); err != nil {
					t.Fatal(err)
				}
			} else if out, err = StringifyWith(input, opts); err != nil {
				t.Fatal(err)
			}
			if out != c.Text {
				t.Fatalf("stringify differs:\n got %q\nwant %q", out, c.Text)
			}
			if !opts.Colors { // the output must parse back to the same value
				back, err := parse(c.Text)
				if err != nil {
					t.Fatal(err)
				}
				if got := generic(mustTaggedJSON(back)); !reflect.DeepEqual(got, want) {
					t.Fatalf("re-parse differs:\n got %v\nwant %v", got, want)
				}
			}
		})
	}
}

// ------------------------------------------------------------------ cross-check with JS

type bridgeReq struct {
	Op      string          `json:"op"`
	Text    *string         `json:"text,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Options map[string]any  `json:"options,omitempty"`
	RT      bool            `json:"rt,omitempty"`
}

type bridgeRes struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func runJS(t *testing.T, reqs []bridgeReq) []bridgeRes {
	root := suite(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	in, _ := json.Marshal(reqs)
	cmd := exec.Command("node", filepath.Join("testdata", "js_bridge.mjs"), root)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("js bridge: %v\n%s", err, stderr.String())
	}
	var res []bridgeRes
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

var crossOptionSets = []map[string]any{
	{}, {"quotes": "all"}, {"quotes": "keys"}, {"separator": true}, {"condense": 60.0}, {"bracesSameLine": true},
	{"emitRootBraces": false}, {"multiline": "off"}, {"multiline": "no-tabs"}, {"sortProps": true},
	{"space": "\t"}, {"quoteChar": "'"},
}

var pieces = []string{
	"a", "b", "z", "Hello", "world", " ", "  ", ",", ", ", "}", "]", "{", "[", ":", "#", "//", "/*", "*/",
	"'", `"`, "'''", `\`, `\n`, "\n", "\t", "\r", "x y", "é", "⊄", "😀", "\u200b", "\ufeff", "\u2028",
	"true", "null", "NaN", "Infinity", "-0", "1", "0.5", "1e5", "2024-01-15", "T10:30Z", `\frac{a,b}{c}`,
}

func randStr(r *rand.Rand) string {
	var b strings.Builder
	for i := r.Intn(5); i > 0; i-- {
		b.WriteString(pieces[r.Intn(len(pieces))])
	}
	return b.String()
}

var floats = []float64{0, math.Copysign(0, -1), 0.1, 1.5, -2.5e-7, 1e21, 1e-7, 123.456, 5e-324,
	1.7976931348623157e308, math.NaN(), math.Inf(1), math.Inf(-1), 100, 1e16, 0.000001}

func randScalar(r *rand.Rand) any {
	switch r.Intn(10) {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2:
		return float64(r.Int63n(1<<53)) * float64(1-2*r.Intn(2))
	case 3:
		return floats[r.Intn(len(floats))]
	case 4:
		return (r.Float64()*2 - 1) * 1e6 * math.Pow10(r.Intn(61)-30)
	case 5:
		base := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
		ms := r.Int63n(int64(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC).Sub(base) / time.Millisecond))
		d := base.Add(time.Duration(ms) * time.Millisecond)
		if r.Intn(3) == 0 {
			d = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		}
		return &Date{Time: d}
	}
	return randStr(r)
}

func randKey(r *rand.Rand) string {
	switch r.Intn(4) {
	case 0:
		return strconv.Itoa(r.Intn(21))
	case 1:
		return "0" + strconv.Itoa(r.Intn(10))
	case 2:
		return "k" + strconv.Itoa(r.Intn(10))
	}
	return randStr(r)
}

func randValue(r *rand.Rand, depth int) any {
	k := r.Intn(10)
	if depth < 3 && k < 3 {
		a := &Array{Items: []any{}}
		for i := r.Intn(5); i > 0; i-- {
			a.Items = append(a.Items, randValue(r, depth+1))
		}
		return a
	}
	if depth < 3 && k < 6 {
		o := NewObject()
		for i := r.Intn(5); i > 0; i-- {
			o.Set(randKey(r), randValue(r, depth+1))
		}
		return o
	}
	return randScalar(r)
}

func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return n
	}
	return def
}

// Python и JS пишут одинаковые данные одинаково, а каждая реализация читает текст другой без потерь.
func TestCrossRandomValues(t *testing.T) {
	r := rand.New(rand.NewSource(int64(envInt("MKON_CROSS_SEED", 20261025))))
	count := envInt("MKON_CROSS_COUNT", 500)
	var vals []any
	var reqs []bridgeReq
	var goText []string
	for i := 0; i < count; i++ {
		v := randValue(r, 0)
		o := crossOptionSets[i%len(crossOptionSets)]
		s, err := StringifyWith(v, jsOptions(o))
		if err != nil {
			t.Fatalf("go stringify: %v", err)
		}
		vals, goText = append(vals, v), append(goText, s)
		reqs = append(reqs, bridgeReq{Op: "stringify", Value: mustTaggedJSON(v), Options: o},
			bridgeReq{Op: "parse", Text: &goText[len(goText)-1]})
	}
	res := runJS(t, reqs)
	fails := 0
	for i, v := range vals {
		jt, jp := res[2*i], res[2*i+1]
		if !jt.OK || !jp.OK {
			t.Errorf("#%d js error: %s %s", i, jt.Error, jp.Error)
			fails++
			continue
		}
		var jsText string
		_ = json.Unmarshal(jt.Result, &jsText)
		want := generic(mustTaggedJSON(v))
		if jsText != goText[i] {
			t.Errorf("#%d text differs (opts %v):\n go %q\n js %q", i, crossOptionSets[i%len(crossOptionSets)], goText[i], jsText)
			fails++
		}
		if got := generic(jp.Result); !reflect.DeepEqual(got, want) {
			t.Errorf("#%d js.parse(go text) differs", i)
			fails++
		}
		back, err := Parse(jsText)
		if err != nil {
			t.Errorf("#%d go.parse(js text): %v", i, err)
			fails++
		} else if got := generic(mustTaggedJSON(back)); !reflect.DeepEqual(got, want) {
			t.Errorf("#%d go.parse(js text) differs", i)
			fails++
		}
		if fails > 15 {
			t.FailNow()
		}
	}
}

var tokens = []string{"{", "}", "[", "]", ",", ":", `"`, "'", "'''", "/", "//", "/*", "*/", "\n", "\r\n", " ", "  ",
	"\t", "a", "key", "1", "-0", "1.5e3", "#", `\`, `\n`, `A`, "x y", "true", "null", "NaN", "-Infinity",
	"2024-01-15", "T10:30Z", "\u00a0", "😀", "\x00", "\ufeff"}

// Случайный, в основном невалидный текст: одинаковые ошибки (с тем же сообщением), данные и формат.
func TestCrossRandomTexts(t *testing.T) {
	r := rand.New(rand.NewSource(int64(envInt("MKON_CROSS_SEED", 20261025))))
	n := envInt("MKON_CROSS_COUNT", 500) * 10
	texts := make([]string, n)
	var reqs []bridgeReq
	for i := range texts {
		var b strings.Builder
		for j := r.Intn(26); j > 0; j-- {
			b.WriteString(tokens[r.Intn(len(tokens))])
		}
		texts[i] = b.String()
	}
	for i := range texts {
		reqs = append(reqs, bridgeReq{Op: "parse", Text: &texts[i], RT: i%3 == 0})
	}
	for i := range texts {
		reqs = append(reqs, bridgeReq{Op: "format", Text: &texts[i], RT: i%3 == 0})
	}
	res := runJS(t, reqs)
	fails := 0
	for i, text := range texts {
		rt := i%3 == 0
		jp, jf := res[i], res[n+i]
		v, err := ParseWith(text, ParseOptions{KeepComments: rt})
		switch {
		case (err == nil) != jp.OK:
			t.Errorf("%q: go err=%v / js %+v", text, err, jp)
		case err != nil:
			if msg := err.(*SyntaxError).Msg; !strings.HasPrefix(jp.Error, msg) {
				t.Errorf("%q: error differs: go %q / js %q", text, err, jp.Error)
			}
		default:
			if got, want := generic(mustTaggedJSON(v)), generic(jp.Result); !reflect.DeepEqual(got, want) {
				t.Errorf("%q: data differs: go %v / js %v", text, got, want)
			} else {
				o := DefaultOptions()
				o.KeepComments = rt
				out, err := StringifyWith(v, o)
				var jsOut string
				_ = json.Unmarshal(jf.Result, &jsOut)
				if (err == nil) != jf.OK || (err == nil && out != jsOut) {
					t.Errorf("%q: format differs: go %q (%v) / js %+v", text, out, err, jf)
				}
			}
		}
		if t.Failed() {
			fails++
			if fails > 15 {
				t.FailNow()
			}
		}
	}
}
