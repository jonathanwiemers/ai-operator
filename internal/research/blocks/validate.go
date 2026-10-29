package blocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxProblems caps the problems an error lists, so a repair prompt stays short.
const maxProblems = 50

// Errors lists the problems of an invalid result, one per line, each with
// its path (blocks[3].items[7].url), to be fed back to the agent.
type Errors []string

func (e Errors) Error() string { return strings.Join(e, "\n") }

// Validate checks an agent's answer against schema v1. It returns the clean
// result, warnings (unknown block types are dropped) and an Errors listing
// every problem. On error the result is empty: nothing of an invalid answer
// may reach the database or the browser.
func Validate(raw []byte) (Result, []string, error) {
	if len(raw) > MaxResultBytes {
		return Result{}, nil, Errors{fmt.Sprintf("result: %d bytes, at most %d", len(raw), MaxResultBytes)}
	}
	if !utf8.Valid(raw) {
		return Result{}, nil, Errors{"result: not valid UTF-8"}
	}
	if t := bytes.TrimLeft(raw, " \t\r\n"); len(t) == 0 || t[0] != '{' {
		return Result{}, nil, Errors{"result: not a JSON object"}
	}
	var top struct {
		SchemaVersion int               `json:"schemaVersion"`
		Title         string            `json:"title"`
		Summary       string            `json:"summary"`
		Blocks        []json.RawMessage `json:"blocks"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&top); err != nil {
		return Result{}, nil, Errors{"result: " + err.Error()}
	}
	if _, err := dec.Token(); err != io.EOF {
		return Result{}, nil, Errors{"result: data after the JSON object"}
	}
	if top.SchemaVersion != SchemaVersion {
		return Result{}, nil, Errors{fmt.Sprintf("schemaVersion: %d, want %d", top.SchemaVersion, SchemaVersion)}
	}

	c := &checker{}
	c.text("title", top.Title, MaxTitle, false, true)
	c.text("summary", top.Summary, MaxSummary, true, false)
	if len(top.Blocks) > MaxBlocks {
		c.fail("blocks", "%d blocks, at most %d", len(top.Blocks), MaxBlocks)
	}
	res := Result{SchemaVersion: SchemaVersion, Title: top.Title, Summary: top.Summary, Blocks: []Block{}}
	for i, data := range top.Blocks {
		path := fmt.Sprintf("blocks[%d]", i)
		var b Block
		switch err := json.Unmarshal(data, &b); {
		case err != nil:
			c.fail(path, "%v", err)
		case b.Type == "":
			c.fail(path, "type is required")
		case b.body() == nil:
			c.warnings = append(c.warnings, fmt.Sprintf("%s: unknown type %.40q dropped", path, b.Type))
		default:
			c.block(path, &b)
			res.Blocks = append(res.Blocks, b)
		}
	}
	if c.items > MaxItems {
		c.fail("blocks", "%d items in total, at most %d", c.items, MaxItems)
	}
	if len(c.errs) > 0 {
		if c.dropped > 0 {
			c.errs = append(c.errs, fmt.Sprintf("and %d more problems", c.dropped))
		}
		return Result{}, c.warnings, c.errs
	}
	return res, c.warnings, nil
}

type checker struct {
	errs     Errors
	dropped  int
	warnings []string
	items    int
}

func (c *checker) fail(path, format string, args ...any) {
	if len(c.errs) == maxProblems {
		c.dropped++
		return
	}
	c.errs = append(c.errs, path+": "+fmt.Sprintf(format, args...))
}

func (c *checker) block(path string, b *Block) {
	switch b.Type {
	case TypeHeading:
		h := b.Heading
		c.text(path+".text", h.Text, MaxTitle, false, true)
		if h.Level == 0 {
			h.Level = 2
		}
		if h.Level != 2 && h.Level != 3 {
			c.fail(path+".level", "%d, want 2 or 3", h.Level)
		}
	case TypeMarkdown:
		c.markdown(path+".text", b.Markdown.Text)
	case TypeItems:
		c.list(path+".items", len(b.Items.Items), MaxItems)
		c.items += len(b.Items.Items)
		for i := range b.Items.Items {
			it, p := &b.Items.Items[i], fmt.Sprintf("%s.items[%d]", path, i)
			c.text(p+".topic", it.Topic, MaxTopic, false, false)
			c.text(p+".title", it.Title, MaxTitle, false, true)
			c.text(p+".summary", it.Summary, MaxSummary, true, false)
			c.url(p+".url", it.URL)
			c.text(p+".source", it.Source, MaxTitle, false, false)
			if it.Importance == 0 {
				it.Importance = 2
			}
			if it.Importance < 1 || it.Importance > 3 {
				c.fail(p+".importance", "%d, want 1, 2 or 3", it.Importance)
			}
		}
	case TypeTable:
		t := b.Table
		c.list(path+".columns", len(t.Columns), MaxColumns)
		c.list(path+".rows", len(t.Rows), MaxRows)
		for i, col := range t.Columns {
			c.text(fmt.Sprintf("%s.columns[%d]", path, i), col, MaxCell, false, false)
		}
		for i, row := range t.Rows {
			p := fmt.Sprintf("%s.rows[%d]", path, i)
			if len(row) != len(t.Columns) {
				c.fail(p, "%d cells, want one per column (%d)", len(row), len(t.Columns))
				continue
			}
			for j, cell := range row {
				c.text(fmt.Sprintf("%s[%d]", p, j), cell, MaxCell, false, false)
			}
		}
	case TypeStats:
		c.list(path+".stats", len(b.Stats.Stats), MaxEntries)
		for i, s := range b.Stats.Stats {
			p := fmt.Sprintf("%s.stats[%d]", path, i)
			c.text(p+".label", s.Label, MaxTitle, false, true)
			c.text(p+".value", s.Value, MaxTitle, false, true)
			c.text(p+".delta", s.Delta, MaxTitle, false, false)
		}
	case TypeLinks:
		c.list(path+".links", len(b.Links.Links), MaxEntries)
		for i, l := range b.Links.Links {
			p := fmt.Sprintf("%s.links[%d]", path, i)
			c.text(p+".title", l.Title, MaxTitle, false, true)
			c.url(p+".url", l.URL)
		}
	}
}

func (c *checker) list(path string, n, max int) {
	if n == 0 {
		c.fail(path, "must not be empty")
	} else if n > max {
		c.fail(path, "%d entries, at most %d", n, max)
	}
}

// text checks a string's length in characters and that it has no control
// characters but, if multiline, newlines and tabs.
func (c *checker) text(path, s string, max int, multiline, required bool) {
	if required && strings.TrimSpace(s) == "" {
		c.fail(path, "is required")
	}
	if n := utf8.RuneCountInString(s); n > max {
		c.fail(path, "%d characters, at most %d", n, max)
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) {
			c.fail(path, "control character %U", r)
			return
		}
	}
}

func (c *checker) url(path, s string) {
	c.text(path, s, MaxURL, false, true)
	if s != "" {
		if err := checkURL(s); err != nil {
			c.fail(path, "%v", err)
		}
	}
}

// checkURL accepts absolute http and https URLs with a host and no userinfo.
func checkURL(s string) error {
	u, err := url.Parse(s)
	switch {
	case err != nil || u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("%.80q is not an absolute http or https URL", s)
	case u.Hostname() == "":
		return fmt.Errorf("%.80q has no host", s)
	case u.User != nil:
		return fmt.Errorf("%.80q has user info", s)
	case strings.ContainsAny(s, ` \`):
		return fmt.Errorf("%.80q contains a space or backslash", s)
	}
	return nil
}

// Degraded wraps an answer that is still invalid after the repair round:
// one markdown block with the answer, cut to fit, inside a fenced code
// block, so it renders as text whatever it contains.
func Degraded(raw string) Result {
	raw = raw[:min(len(raw), MaxMarkdownBytes)]
	text := strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return -1
		case unicode.IsControl(r) && r != '\n' && r != '\t':
			return utf8.RuneError
		}
		return r
	}, strings.ToValidUTF8(raw, string(utf8.RuneError)))
	const cut = "\n\n(truncated)"
	note := ""
	for {
		// The fence is longer than any backtick run in the text, so
		// nothing in it closes the fence.
		fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
		limit := MaxMarkdownBytes - 2*len(fence) - 2 - len(cut)
		if len(text) <= limit {
			md := fence + "\n" + text + "\n" + fence + note
			return Result{
				SchemaVersion: SchemaVersion,
				Title:         "Unstructured answer",
				Summary:       "The answer did not match the block schema and is shown as received.",
				Blocks:        []Block{{Type: TypeMarkdown, Markdown: &Markdown{Text: md}}},
			}
		}
		text, note = cutUTF8(text, max(0, limit)), cut
	}
}

func longestRun(s string, ch byte) int {
	longest, n := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == ch {
			n++
			longest = max(longest, n)
		} else {
			n = 0
		}
	}
	return longest
}

func cutUTF8(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
