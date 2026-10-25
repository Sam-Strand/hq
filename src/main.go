package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/Sam-Strand/hq/mkon"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `hq — jq-like query tool for mkon

Usage:
    hq [options] <jq-filter> [file]
    cat file.mkon | hq [options] <jq-filter>

Options:
    -r, --raw-output    raw string output (no JSON quotes)
    -i, --in-place      write result back as mkon, keeping comments (requires file)
    -c, --compact       compact JSON output
    -e, --exit-status   exit 1 if last output was null/false
    -s, --slurp         read entire input into an array
        --arg NAME VAL  set $NAME to string VAL
        --argjson N VAL set $N to JSON VAL
        --args          remaining args are strings -> $ARGS.positional
        --jsonargs      remaining args are JSON    -> $ARGS.positional
    -h, --help          show this help
    -V, --version       show version

JSON has no dates or NaN/Infinity, so jq sees them as strings:
    2024-01-15, 2024-01-15T10:30:00Z  ->  "2024-01-15", "2024-01-15T10:30:00Z"
    NaN, Infinity, -Infinity          ->  "NaN", "Infinity", "-Infinity"
With -i they become dates/numbers again where the original file had them.

Examples:
    hq '.repos | keys[]' repos.mkon
    hq -r '.repos.foo.url' repos.mkon
    hq -r --arg k foo '.repos[$k].url' repos.mkon
    hq -i '.repos.foo.branch = "dev"' repos.mkon
`)
}

type jqOpts struct {
	raw      bool
	exitStat bool
	slurp    bool
	compact  bool
	inPlace  bool
	jqArgs   []string // накопленные аргументы для jq: ("--arg", "k", "v", ...)
	filter   string
	file     string
	jsonArgs []string // значения для --args / --jsonargs
	mode     string   // "", "args", "jsonargs"
}

func parseArgs(argv []string) (*jqOpts, error) {
	o := &jqOpts{}
	i := 0
	endOpts := false

	need := func(n int) error {
		if i+n >= len(argv) {
			return fmt.Errorf("option %s requires %d argument(s)", argv[i], n)
		}
		return nil
	}

	for i < len(argv) {
		a := argv[i]
		if endOpts || !strings.HasPrefix(a, "-") || a == "-" {
			switch {
			case o.filter == "":
				o.filter = a
			case o.mode != "":
				// после --args / --jsonargs всё позиционное — это значения
				o.jsonArgs = append(o.jsonArgs, a)
			case o.file == "":
				o.file = a
			default:
				return nil, fmt.Errorf("too many arguments")
			}
			i++
			continue
		}

		switch a {
		case "--":
			endOpts = true
			i++
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "-V", "--version":
			fmt.Println("hq", version)
			os.Exit(0)
		case "-r", "--raw-output":
			o.raw = true
			o.jqArgs = append(o.jqArgs, "-r")
			i++
		case "-e", "--exit-status":
			o.exitStat = true
			o.jqArgs = append(o.jqArgs, "-e")
			i++
		case "-s", "--slurp":
			o.slurp = true
			o.jqArgs = append(o.jqArgs, "-s")
			i++
		case "-c", "--compact":
			o.compact = true
			o.jqArgs = append(o.jqArgs, "-c")
			i++
		case "-i", "--in-place":
			o.inPlace = true
			i++
		case "--arg", "--argjson":
			if err := need(2); err != nil {
				return nil, err
			}
			o.jqArgs = append(o.jqArgs, a, argv[i+1], argv[i+2])
			i += 3
		case "--args":
			o.mode = "args"
			i++
		case "--jsonargs":
			o.mode = "jsonargs"
			i++
		default:
			return nil, fmt.Errorf("unknown option: %s", a)
		}
	}

	if o.filter == "" {
		return nil, fmt.Errorf("filter is required")
	}
	if o.inPlace {
		switch {
		case o.file == "" || o.file == "-":
			return nil, fmt.Errorf("--in-place requires a file argument")
		case o.raw:
			return nil, fmt.Errorf("--in-place cannot be combined with --raw-output")
		case o.mode != "":
			return nil, fmt.Errorf("--in-place cannot be combined with --args/--jsonargs (the file would be ambiguous)")
		}
	}
	return o, nil
}

// runJQ прогоняет JSON через jq и возвращает stdout.
func runJQ(o *jqOpts, jsonInput []byte) ([]byte, bool, error) {
	args := append([]string{}, o.jqArgs...)
	args = append(args, o.filter)
	if o.mode != "" {
		// --args/--jsonargs идут после фильтра, иначе jq сочтёт значения файлами
		args = append(args, "--"+o.mode)
		args = append(args, o.jsonArgs...)
	}

	cmd := exec.Command("jq", args...)
	cmd.Stdin = bytes.NewReader(jsonInput)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	// jq -e: код 1 значит "result was null/false, no error"
	exit1 := false
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 && o.exitStat {
		exit1, err = true, nil
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, false, fmt.Errorf("jq: %s", msg)
	}
	return stdout.Bytes(), exit1, nil
}

// restore возвращает типы mkon, которых нет в JSON, и комментарии исходного файла:
// строка на месте даты снова становится датой (если это корректная ISO-дата),
// "NaN"/"Infinity"/"-Infinity" на месте числа — числом; комментарии переносятся по ключам и индексам.
func restore(orig, v any) any {
	switch x := v.(type) {
	case string:
		switch o := orig.(type) {
		case *mkon.Date:
			if s, err := mkon.FormatDate(o); err == nil && s == x {
				return o // без изменений — вместе с комментариями
			}
			if d, err := mkon.Parse(x); err == nil {
				if _, ok := d.(*mkon.Date); ok {
					return d
				}
			}
		case float64:
			switch x {
			case "NaN":
				return math.NaN()
			case "Infinity":
				return math.Inf(1)
			case "-Infinity":
				return math.Inf(-1)
			}
		}
	case *mkon.Object:
		if o, ok := orig.(*mkon.Object); ok {
			x.Comments = o.Comments // ключи, которых больше нет, пропускаются при записи
			for _, k := range x.Keys() {
				if ov, ok := o.Get(k); ok {
					nv, _ := x.Get(k)
					x.Set(k, restore(ov, nv))
				}
			}
		}
	case *mkon.Array:
		if o, ok := orig.(*mkon.Array); ok {
			x.Comments = o.Comments
			for i := range x.Items {
				if i < len(o.Items) {
					x.Items[i] = restore(o.Items[i], x.Items[i])
				}
			}
		}
	}
	return v
}

// startsWithBrace — начинается ли текст (после пробелов и комментариев) с '{'.
func startsWithBrace(s string) bool {
	for {
		s = strings.TrimLeft(s, " \t\r\n\ufeff")
		switch {
		case strings.HasPrefix(s, "//"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i:]
			} else {
				return false
			}
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s[2:], "*/"); i >= 0 {
				s = s[i+4:]
			} else {
				return false
			}
		default:
			return strings.HasPrefix(s, "{")
		}
	}
}

func fail(code int, format string, a ...any) {
	fmt.Fprintf(os.Stderr, "hq: "+format+"\n", a...)
	os.Exit(code)
}

func main() {
	o, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "hq: %v\n", err)
		usage()
		os.Exit(2)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		fail(2, "jq not found in PATH. Install it: sudo apt install jq")
	}

	// 1. Источник: файл или stdin
	var data []byte
	if o.file != "" && o.file != "-" {
		data, err = os.ReadFile(o.file)
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fail(1, "read input: %v", err)
	}

	// 2. mkon -> модель (с комментариями, если файл будет перезаписан)
	doc, err := mkon.ParseWith(string(data), mkon.ParseOptions{KeepComments: o.inPlace})
	if err != nil {
		fail(1, "parse mkon: %v", err)
	}

	// 3. -> JSON -> jq
	jsonBytes, err := mkon.ToJSON(doc, "")
	if err != nil {
		fail(1, "encode to JSON: %v", err)
	}
	out, exit1, err := runJQ(o, jsonBytes)
	if err != nil {
		fail(1, "%v", err)
	}

	if !o.inPlace {
		os.Stdout.Write(out)
		if exit1 {
			os.Exit(1)
		}
		return
	}

	// 4. -i: результат jq -> mkon с типами и комментариями исходного файла
	results, err := mkon.FromJSONStream(out)
	if err != nil {
		fail(1, "cannot write in-place, jq output is not JSON: %v", err)
	}
	if len(results) != 1 {
		fail(1, "cannot write in-place, jq produced %d results (need exactly 1)", len(results))
	}
	opts := mkon.DefaultOptions()
	opts.KeepComments = true
	opts.BracesSameLine = true // как раньше у hq (hjson-go)
	if _, isObj := doc.(*mkon.Object); isObj && !startsWithBrace(string(data)) {
		opts.RootBraces = false // файл был без корневых скобок — так и оставляем
	}
	if bytes.Contains(data, []byte("\r\n")) {
		opts.EOL = "\r\n"
	}
	text, err := mkon.StringifyWith(restore(doc, results[0]), opts)
	if err != nil {
		fail(1, "encode mkon: %v", err)
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(o.file); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(o.file, []byte(text), mode); err != nil {
		fail(1, "write %s: %v", o.file, err)
	}
	if exit1 {
		os.Exit(1)
	}
}
