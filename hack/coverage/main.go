// Command coverage checks Go statement coverage per package against a
// committed baseline.
//
// It reads a `go test -coverprofile` file, computes covered statements /
// statements for every package and prints a table of all packages with
// their baseline, current value, delta and status (ok, drop, new, gone). It
// exits 1 when a package is more than 0.2 points below its baseline. With
// -write it writes the current values as the new baseline instead.
//
//	go run ./hack/coverage -profile coverage.out -baseline hack/coverage-baseline.txt
//	go run ./hack/coverage -profile coverage.out -write hack/coverage-baseline.txt
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// maxDropTenths is the largest drop, in tenths of a point, that still passes.
const maxDropTenths = 2

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fl.SetOutput(stderr)
	profile := fl.String("profile", "coverage.out", "go test cover profile to read")
	baseline := fl.String("baseline", "hack/coverage-baseline.txt", "baseline file to compare with")
	write := fl.String("write", "", "write the measured values to this baseline file instead of failing on drops")
	module := fl.String("module", "", "module path stripped from package names (default: the module line of ./go.mod)")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "coverage:", err)
		return 2
	}
	if *module == "" {
		m, err := moduleFromGoMod("go.mod")
		if err != nil {
			return fail(err)
		}
		*module = m
	}
	f, err := os.Open(*profile)
	if err != nil {
		return fail(err)
	}
	now, err := parseProfile(f, *module)
	_ = f.Close()
	if err != nil {
		return fail(fmt.Errorf("%s: %w", *profile, err))
	}
	base, err := readBaseline(*baseline)
	if err != nil {
		return fail(err)
	}
	rows := compare(base, now)
	printTable(stdout, rows)
	if *write != "" {
		if err := writeBaseline(*write, now); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "\nwrote %d packages to %s\n", len(now), *write)
		return 0
	}
	var drops []string
	for _, r := range rows {
		if r.status == statusDrop {
			drops = append(drops, fmt.Sprintf("%s (%s)", r.pkg, fmtDelta(r.now-r.base)))
		}
	}
	if len(drops) > 0 {
		fmt.Fprintf(stdout, "\ncoverage dropped more than %s points below the baseline in %s\n",
			fmtTenths(maxDropTenths), strings.Join(drops, ", "))
		return 1
	}
	return 0
}

// moduleFromGoMod returns the module path of a go.mod file.
func moduleFromGoMod(name string) (string, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("module path: %w (set -module)", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", fmt.Errorf("no module line in %s (set -module)", name)
}

// block is one profile entry: its statement count and the highest hit count.
type block struct {
	stmts int
	count int64
}

// parseProfile reads a cover profile and returns the coverage of every
// package in tenths of a percent, keyed by the package's path relative to
// module ("." for the module root). Blocks listed more than once (one entry
// per test binary that covers the package) count once, with the highest
// count.
func parseProfile(r io.Reader, module string) (map[string]int, error) {
	blocks := map[string]block{} // key: file:position
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if lineNo == 1 {
			if !strings.HasPrefix(line, "mode: ") {
				return nil, fmt.Errorf("line 1: want a mode: line, got %q", line)
			}
			continue
		}
		// file:startLine.startCol,endLine.endCol numStmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("line %d: want 3 fields, got %q", lineNo, line)
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon <= 0 {
			return nil, fmt.Errorf("line %d: no file:position in %q", lineNo, line)
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("line %d: statements: %w", lineNo, err)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: count: %w", lineNo, err)
		}
		b, seen := blocks[fields[0]]
		if !seen || count > b.count {
			blocks[fields[0]] = block{stmts: stmts, count: count}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if lineNo == 0 {
		return nil, errors.New("empty profile")
	}
	type total struct{ covered, all int }
	pkgs := map[string]*total{}
	for key, b := range blocks {
		file := key[:strings.LastIndex(key, ":")]
		pkg := packageName(path.Dir(file), module)
		t := pkgs[pkg]
		if t == nil {
			t = &total{}
			pkgs[pkg] = t
		}
		t.all += b.stmts
		if b.count > 0 {
			t.covered += b.stmts
		}
	}
	out := make(map[string]int, len(pkgs))
	for pkg, t := range pkgs {
		if t.all == 0 {
			out[pkg] = 0
			continue
		}
		out[pkg] = int(math.Round(float64(t.covered) * 1000 / float64(t.all)))
	}
	return out, nil
}

// packageName names an import path relative to module.
func packageName(importPath, module string) string {
	if importPath == module {
		return "."
	}
	if rest, ok := strings.CutPrefix(importPath, module+"/"); ok {
		return rest
	}
	return importPath
}

// readBaseline reads `package percent` lines; a missing file is an empty
// baseline, so every package is new.
func readBaseline(name string) (map[string]int, error) {
	data, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want `package percent`, got %q", name, i+1, line)
		}
		pct, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, i+1, err)
		}
		out[fields[0]] = int(math.Round(pct * 10))
	}
	return out, nil
}

// writeBaseline writes one `package percent` line per package, sorted.
func writeBaseline(name string, now map[string]int) error {
	var b strings.Builder
	for _, pkg := range sortedKeys(now) {
		fmt.Fprintf(&b, "%s %s\n", pkg, fmtTenths(now[pkg]))
	}
	return os.WriteFile(name, []byte(b.String()), 0o644)
}

const (
	statusOK   = "ok"
	statusDrop = "drop"
	statusNew  = "new"
	statusGone = "gone"
)

// row is one package of the table; base and now are tenths of a percent.
type row struct {
	pkg       string
	base, now int
	status    string
}

// compare returns one row per package in the baseline or the profile,
// sorted by package.
func compare(base, now map[string]int) []row {
	all := map[string]bool{}
	for p := range base {
		all[p] = true
	}
	for p := range now {
		all[p] = true
	}
	rows := make([]row, 0, len(all))
	for _, pkg := range sortedKeys(all) {
		b, inBase := base[pkg]
		n, inNow := now[pkg]
		r := row{pkg: pkg, base: b, now: n}
		switch {
		case !inBase:
			r.status = statusNew
		case !inNow:
			r.status = statusGone
		case b-n > maxDropTenths:
			r.status = statusDrop
		default:
			r.status = statusOK
		}
		rows = append(rows, r)
	}
	return rows
}

func printTable(w io.Writer, rows []row) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "package\tbaseline\tnow\tdelta\tstatus")
	for _, r := range rows {
		base, now, delta := fmtTenths(r.base), fmtTenths(r.now), fmtDelta(r.now-r.base)
		switch r.status {
		case statusNew:
			base, delta = "-", "-"
		case statusGone:
			now, delta = "-", "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.pkg, base, now, delta, r.status)
	}
	_ = tw.Flush()
}

func fmtTenths(t int) string {
	sign := ""
	if t < 0 {
		sign, t = "-", -t
	}
	return fmt.Sprintf("%s%d.%d", sign, t/10, t%10)
}

func fmtDelta(t int) string {
	if t > 0 {
		return "+" + fmtTenths(t)
	}
	return fmtTenths(t)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
