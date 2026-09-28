package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "os/exec"
    "strings"

    "github.com/hjson/hjson-go/v4"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `hq — jq-like query tool for HJSON

Usage:
    hq [options] <jq-filter> [file]
    cat file.hjson | hq [options] <jq-filter>

Options:
    -r, --raw-output    raw string output (no JSON quotes)
    -i, --in-place      write result back as HJSON (requires file)
    -c, --compact       compact JSON output
    -e, --exit-status   exit 1 if last output was null/false
    -s, --slurp         read entire input into an array
        --arg NAME VAL  set $NAME to string VAL
        --argjson N VAL set $N to JSON VAL
        --args          remaining args are strings -> $ARGS.positional
        --jsonargs      remaining args are JSON    -> $ARGS.positional
    -h, --help          show this help
    -V, --version       show version

Examples:
    hq '.repos | keys[]' repos.hjson
    hq -r '.repos.foo.url' repos.hjson
    hq -r --arg k foo '.repos[$k].url' repos.hjson
    hq -i '.repos.foo.branch = "dev"' repos.hjson
`)
}

type jqOpts struct {
    raw      bool
    exitStat bool
    slurp    bool
    compact  bool
    inPlace  bool
    // накопленные пары для jq: ("--arg", "k", "v", ...)
    jqArgs   []string
    filter   string
    file     string
    jsonArgs []string // значения для --args / --jsonargs
    mode     string   // "", "args", "jsonargs"
}

func parseArgs(argv []string) (*jqOpts, error) {
    o := &jqOpts{}
    i := 0
    endOpts := false

    need := func(n int, what string) error {
        if i+n >= len(argv) {
            return fmt.Errorf("option %s requires %d argument(s)", argv[i], n)
        }
        return nil
    }

    for i < len(argv) {
        a := argv[i]
        if endOpts || !strings.HasPrefix(a, "-") || a == "-" {
            switch {
            case o.mode != "":
                // после --args / --jsonargs всё позиционное — это значения
                o.jsonArgs = append(o.jsonArgs, a)
            case o.filter == "":
                o.filter = a
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
            i++
        case "-i", "--in-place":
            o.inPlace = true
            i++
        case "--arg", "--argjson":
            if err := need(2, a); err != nil {
                return nil, err
            }
            o.jqArgs = append(o.jqArgs, a, argv[i+1], argv[i+2])
            i += 3
        case "--args":
            o.mode = "args"
            o.jqArgs = append(o.jqArgs, "--args")
            i++
        case "--jsonargs":
            o.mode = "jsonargs"
            o.jqArgs = append(o.jqArgs, "--jsonargs")
            i++
        default:
            return nil, fmt.Errorf("unknown option: %s", a)
        }
    }

    // позиционные аргументы после --args/--jsonargs уже собраны; иначе — сюда
    if o.mode != "" && o.filter == "" {
        return nil, fmt.Errorf("--args/--jsonargs requires a filter")
    }

    if o.filter == "" {
        return nil, fmt.Errorf("filter is required")
    }
    return o, nil
}

func checkJQ() error {
    _, err := exec.LookPath("jq")
    if err != nil {
        return fmt.Errorf("jq not found in PATH. Install it: sudo apt install jq")
    }
    return nil
}

// loadHJSON читает HJSON из reader и возвращает интерфейс.
func loadHJSON(r io.Reader) (interface{}, error) {
    data, err := io.ReadAll(r)
    if err != nil {
        return nil, fmt.Errorf("read input: %w", err)
    }
    var v interface{}
    if err := hjson.Unmarshal(data, &v); err != nil {
        return nil, fmt.Errorf("parse HJSON: %w", err)
    }
    return v, nil
}

// runJQ прогоняет JSON через jq и возвращает stdout.
func runJQ(o *jqOpts, jsonInput []byte) ([]byte, bool, error) {
    args := append([]string{}, o.jqArgs...)
    args = append(args, o.filter)
    if len(o.jsonArgs) > 0 {
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
    if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && o.exitStat {
        exit1 = true
        err = nil
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

// dumpHJSON сериализует объект в HJSON.
func dumpHJSON(v interface{}) ([]byte, error) {
    opts := hjson.DefaultOptions()
    opts.IndentBy = "  "
    return hjson.MarshalWithOptions(v, opts)
}

func main() {
    o, err := parseArgs(os.Args[1:])
    if err != nil {
        fmt.Fprintf(os.Stderr, "hq: %v\n", err)
        usage()
        os.Exit(2)
    }

    if err := checkJQ(); err != nil {
        fmt.Fprintf(os.Stderr, "hq: %v\n", err)
        os.Exit(2)
    }

    // Источник: файл или stdin.
    var (
        input io.Reader
        path  string
    )
    if o.file != "" {
        f, err := os.Open(o.file)
        if err != nil {
            fmt.Fprintf(os.Stderr, "hq: %v\n", err)
            os.Exit(1)
        }
        defer f.Close()
        input = f
        path = o.file
    } else {
        input = os.Stdin
    }

    // 1. HJSON -> interface{}
    obj, err := loadHJSON(input)
    if err != nil {
        fmt.Fprintf(os.Stderr, "hq: %v\n", err)
        os.Exit(1)
    }

    // 2. -> JSON для jq
    jsonBytes, err := json.Marshal(obj)
    if err != nil {
        fmt.Fprintf(os.Stderr, "hq: encode to JSON: %v\n", err)
        os.Exit(1)
    }

    // 3. jq
    out, exit1, err := runJQ(o, jsonBytes)
    if err != nil {
        fmt.Fprintf(os.Stderr, "hq: %v\n", err)
        os.Exit(1)
    }
    if exit1 {
        os.Exit(1)
    }

    // 4. Вывод
    if o.inPlace {
        if path == "" {
            fmt.Fprintln(os.Stderr, "hq: --in-place requires a file argument")
            os.Exit(1)
        }
        var result interface{}
        if err := json.Unmarshal(out, &result); err != nil {
            fmt.Fprintf(os.Stderr, "hq: cannot write in-place, jq output is not JSON: %v\n", err)
            os.Exit(1)
        }
        hjsonBytes, err := dumpHJSON(result)
        if err != nil {
            fmt.Fprintf(os.Stderr, "hq: encode HJSON: %v\n", err)
            os.Exit(1)
        }
        if err := os.WriteFile(path, hjsonBytes, 0644); err != nil {
            fmt.Fprintf(os.Stderr, "hq: write %s: %v\n", path, err)
            os.Exit(1)
        }
        return
    }

    if o.compact && !o.raw {
        var v interface{}
        if err := json.Unmarshal(out, &v); err == nil {
            compactBytes, _ := json.Marshal(v)
            os.Stdout.Write(compactBytes)
            os.Stdout.Write([]byte("\n"))
            return
        }
    }
    os.Stdout.Write(out)
}
