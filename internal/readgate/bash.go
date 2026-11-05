package readgate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// The Bash rule catches the common whole-file dump (`cat a.ts; cat b.ts`),
// it is not a shell parser: whatever it does not understand is allowed.

// word is one shell word after quote removal.
type word struct {
	text string
	// glob: the word has an unquoted *, ? or [.
	glob bool
	// quoted: part of the word was quoted or escaped.
	quoted bool
}

// segment is one command of a `;`, `&&` or newline separated list.
type segment struct {
	words []word
	// complex: the segment has a pipe, redirect, `||`, `&`, subshell,
	// command substitution or variable, so the gate does not judge it.
	complex bool
}

// split tokenizes cmd into segments. ok is false when the quotes are
// unbalanced or the command ends in a backslash.
func split(cmd string) (segs []segment, ok bool) {
	var cur segment
	var b strings.Builder
	var w word
	inWord := false
	endWord := func() {
		if inWord {
			w.text = b.String()
			cur.words = append(cur.words, w)
		}
		b.Reset()
		w, inWord = word{}, false
	}
	endSegment := func() {
		endWord()
		if len(cur.words) > 0 || cur.complex {
			segs = append(segs, cur)
		}
		cur = segment{}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		case c == '\n' || c == ';':
			endSegment()
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&':
			endSegment()
			i++
		case strings.IndexByte("&|<>()`", c) >= 0:
			endWord()
			cur.complex = true
		case c == '$':
			cur.complex = true
			b.WriteByte(c)
			inWord = true
		case c == '#' && !inWord:
			for i+1 < len(cmd) && cmd[i+1] != '\n' {
				i++
			}
		case c == '\\':
			if i+1 >= len(cmd) {
				return nil, false
			}
			i++
			if cmd[i] != '\n' {
				b.WriteByte(cmd[i])
				w.quoted, inWord = true, true
			}
		case c == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
			w.quoted, inWord = true, true
		case c == '"':
			w.quoted, inWord = true, true
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				switch cmd[i] {
				case '\\':
					if i+1 < len(cmd) && strings.IndexByte("\"\\$`\n", cmd[i+1]) >= 0 {
						i++
						if cmd[i] != '\n' {
							b.WriteByte(cmd[i])
						}
						continue
					}
				case '$', '`':
					cur.complex = true
				}
				b.WriteByte(cmd[i])
			}
			if i >= len(cmd) {
				return nil, false
			}
		default:
			if c == '*' || c == '?' || c == '[' {
				w.glob = true
			}
			b.WriteByte(c)
			inWord = true
		}
	}
	endSegment()
	return segs, true
}

type bashInput struct {
	Command string `json:"command"`
}

func decideBash(in Input, minLines int, subagents []string) (bool, string, error) {
	var bi bashInput
	if err := json.Unmarshal(in.ToolInput, &bi); err != nil {
		return false, "", fmt.Errorf("parsing Bash input: %w", err)
	}
	segs, ok := split(bi.Command)
	if !ok {
		return false, "", nil
	}
	dir, dirKnown := in.Cwd, in.Cwd != ""
	for _, seg := range segs {
		if len(seg.words) == 0 {
			continue
		}
		if seg.words[0].text == "cd" {
			dir, dirKnown = cd(seg, dir, dirKnown)
			continue
		}
		if seg.complex {
			continue
		}
		for _, f := range dumpFiles(seg.words, minLines) {
			if !filepath.IsAbs(f.text) && !dirKnown {
				continue
			}
			for _, p := range expand(f, dir) {
				size, over, err := bigFile(resolve(dir, p), minLines)
				if err != nil {
					return false, "", err
				}
				if over {
					return true, denyReason(p, size, minLines, subagents,
						"read only the section you edit with sed -n 'A,Bp' or the Read tool with offset/limit"), nil
				}
			}
		}
	}
	return false, "", nil
}

// cd returns the directory after a `cd DIR` segment. A cd the gate does
// not understand makes the directory unknown, so later relative paths are
// not checked.
func cd(seg segment, dir string, known bool) (string, bool) {
	if seg.complex || len(seg.words) != 2 || seg.words[1].glob ||
		strings.HasPrefix(seg.words[1].text, "-") || strings.HasPrefix(seg.words[1].text, "~") {
		return dir, false
	}
	target := seg.words[1].text
	if filepath.IsAbs(target) {
		return target, true
	}
	return resolve(dir, target), known
}

// expand returns the paths a file word names: the word itself, or the
// matches of an unquoted glob (none when nothing matches). Matches of a
// relative pattern stay relative to dir.
func expand(f word, dir string) []string {
	if !f.glob {
		return []string{f.text}
	}
	if f.quoted {
		return nil
	}
	matches, err := filepath.Glob(resolve(dir, f.text))
	if err != nil {
		return nil
	}
	if !filepath.IsAbs(f.text) {
		for i, m := range matches {
			if rel, err := filepath.Rel(dir, m); err == nil {
				matches[i] = rel
			}
		}
	}
	return matches
}

// catFlags and lessFlags are the short flags that still print the whole
// file.
const (
	catFlags  = "AbeEnstTuv"
	lessFlags = "NRS"
)

var catLong = map[string]bool{
	"--show-all": true, "--number-nonblank": true, "--show-ends": true, "--number": true,
	"--squeeze-blank": true, "--show-tabs": true, "--show-nonprinting": true,
}

// dumpFiles returns the files a whole-file dump segment prints: cat, less
// or more, or head/tail with more than minLines lines. It returns nil for
// any other command, a bounded head/tail and a flag it does not know.
func dumpFiles(words []word, minLines int) []word {
	name := filepath.Base(words[0].text)
	headTail := name == "head" || name == "tail"
	if !headTail && name != "cat" && name != "less" && name != "more" {
		return nil
	}
	var files []word
	lines, unbounded := -1, false
	endOpts := false
	args := words[1:]
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case endOpts || !strings.HasPrefix(a, "-"):
			files = append(files, args[i])
		case a == "-": // stdin
		case a == "--":
			endOpts = true
		case name == "cat" && (catLong[a] || onlyFlags(a[1:], catFlags)):
		case name == "less" && onlyFlags(a[1:], lessFlags):
		case headTail && (a == "-q" || a == "-v" || a == "--quiet" || a == "--silent" || a == "--verbose"):
		case headTail:
			var k string
			switch {
			case a == "-n" || a == "--lines":
				if i+1 >= len(args) {
					return nil
				}
				i++
				k = args[i].text
			case strings.HasPrefix(a, "--lines="):
				k = strings.TrimPrefix(a, "--lines=")
			case strings.HasPrefix(a, "-n"):
				k = a[2:]
			case isDigits(a[1:]):
				k = a[1:]
			default:
				return nil
			}
			n, unb, ok := lineCount(name, k)
			if !ok {
				return nil
			}
			lines, unbounded = n, unb
		default:
			return nil
		}
	}
	if headTail && !unbounded && lines <= minLines {
		// Also head/tail without -n: 10 lines.
		return nil
	}
	return files
}

// lineCount parses head's or tail's -n value: `+K` for tail and `-K` for
// head print all but a part of the file, so they are unbounded.
func lineCount(name, k string) (n int, unbounded, ok bool) {
	sign := ""
	if k != "" && (k[0] == '+' || k[0] == '-') {
		sign, k = k[:1], k[1:]
	}
	if !isDigits(k) {
		return 0, false, false
	}
	n, err := strconv.Atoi(k)
	if err != nil {
		return 0, true, true // too big for an int: unbounded
	}
	if (name == "tail" && sign == "+") || (name == "head" && sign == "-") {
		return n, true, true
	}
	return n, false, true
}

func onlyFlags(s, allowed string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune(allowed, c) {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
