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

const version = "0.1.0"

func usage() {
	fmt.Fprintf(os.Stderr, `hq — jq-like query tool for HJSON

Usage:
    hq [options] <jq-filter> [file]
    cat file.hjson | hq [options] <jq-filter>

Options:
    -r, --raw-output    raw string output (no JSON quotes)
    -i, --in-place      write result back as HJSON (requires file)
    -c, --compact       compact JSON output
    -h, --help          show this help
    -V, --version       show version

Examples:
    hq '.repos | keys[]' repos.hjson
    hq -r '.repos.foo.url' repos.hjson
    hq -i '.repos.foo.branch = "dev"' repos.hjson
`)
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
func runJQ(filter string, jsonInput []byte, raw bool) ([]byte, error) {
	args := []string{}
	if raw {
		args = append(args, "-r")
	}
	args = append(args, filter)

	cmd := exec.Command("jq", args...)
	cmd.Stdin = bytes.NewReader(jsonInput)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("jq: %s", msg)
	}
	return stdout.Bytes(), nil
}

// dumpHJSON сериализует объект в HJSON.
func dumpHJSON(v interface{}) ([]byte, error) {
	opts := hjson.DefaultOptions()
	opts.IndentBy = "  "
	return hjson.MarshalWithOptions(v, opts)
}

func main() {
	// Разбор аргументов вручную (минимум зависимостей).
	var (
		rawOutput bool
		inPlace   bool
		compact   bool
	)

	args := os.Args[1:]
	var filter string
	var file string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			usage()
			os.Exit(0)
		case a == "-V" || a == "--version":
			fmt.Println("hq", version)
			os.Exit(0)
		case a == "-r" || a == "--raw-output":
			rawOutput = true
		case a == "-i" || a == "--in-place":
			inPlace = true
		case a == "-c" || a == "--compact":
			compact = true
		case strings.HasPrefix(a, "-") && filter == "":
			fmt.Fprintf(os.Stderr, "hq: unknown option: %s\n", a)
			usage()
			os.Exit(2)
		default:
			if filter == "" {
				filter = a
			} else if file == "" {
				file = a
			} else {
				fmt.Fprintln(os.Stderr, "hq: too many arguments")
				usage()
				os.Exit(2)
			}
		}
	}

	if filter == "" {
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
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hq: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		input = f
		path = file
	} else {
		input = os.Stdin
	}

	// 1. HJSON -> Go interface{}
	obj, err := loadHJSON(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hq: %v\n", err)
		os.Exit(1)
	}

	// 2. Go interface{} -> JSON для jq
	jsonBytes, err := json.Marshal(obj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hq: encode to JSON: %v\n", err)
		os.Exit(1)
	}

	// 3. Прогон через jq
	out, err := runJQ(filter, jsonBytes, rawOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hq: %v\n", err)
		os.Exit(1)
	}

	// 4. Вывод
	if inPlace {
		if path == "" {
			fmt.Fprintln(os.Stderr, "hq: --in-place requires a file argument")
			os.Exit(1)
		}
		// jq вернул JSON — парсим обратно и пишем HJSON.
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

	if compact && !rawOutput {
		// Переупаковать в компактный JSON.
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
