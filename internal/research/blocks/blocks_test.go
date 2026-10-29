package blocks_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
)

func TestGoodFixturesValidateUnchanged(t *testing.T) {
	files, _ := filepath.Glob("testdata/good/*.json")
	if len(files) < 3 {
		t.Fatalf("want the good fixtures, got %v", files)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			res, warnings, err := blocks.Validate(raw)
			if err != nil || len(warnings) > 0 {
				t.Fatalf("Validate: %v, warnings %v", err, warnings)
			}
			out, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(t, raw, out) {
				t.Errorf("result changed:\n got %s\nwant %s", out, raw)
			}
		})
	}
}

func TestHostileInputs(t *testing.T) {
	cases := map[string]string{
		"md-script":                  "blocks[1].text: line 1: raw HTML",
		"md-img-onerror":             "blocks[1].text: line 1: raw HTML",
		"md-a-href":                  "blocks[1].text: line 1: raw HTML",
		"md-closing-tag":             "blocks[1].text: line 1: raw HTML",
		"md-comment":                 "blocks[1].text: line 1: raw HTML",
		"md-autolink":                "blocks[1].text: line 1: raw HTML",
		"md-javascript-link":         `blocks[1].text: line 1: link "javascript:alert(1)"`,
		"md-javascript-link-upper":   `blocks[1].text: line 1: link "JaVaScRiPt:alert(1)"`,
		"md-data-link":               `blocks[1].text: line 1: link "data:`,
		"md-javascript-reference":    `blocks[1].text: line 3: link "javascript:alert(1)"`,
		"md-reference-in-list":       `blocks[1].text: line 1: link "javascript:alert(1)"`,
		"md-link-next-line":          `blocks[1].text: line 1: link ""`,
		"md-relative-link":           `blocks[1].text: line 1: link "/admin"`,
		"md-protocol-relative-link":  `blocks[1].text: line 1: link "//evil.example/x"`,
		"md-image":                   "blocks[1].text: line 1: images are not allowed",
		"md-image-reference":         "blocks[1].text: line 1: images are not allowed",
		"md-escaped-backtick":        "blocks[1].text: line 1: raw HTML",
		"md-unclosed-code-span":      "blocks[1].text: line 1: raw HTML",
		"md-fence-backtick-info":     "blocks[1].text: line 2: raw HTML",
		"md-fence-in-list-ends":      "blocks[1].text: line 5: raw HTML",
		"md-fence-closed-indented":   "blocks[1].text: line 6: raw HTML",
		"md-control-char":            "blocks[1].text: control character U+0007",
		"md-carriage-return":         "blocks[1].text: control character U+000D",
		"item-javascript-url":        `blocks[0].items[1].url: "javascript:alert(1)" is not an absolute http or https URL`,
		"item-data-url":              `blocks[0].items[1].url: "data:text/html,`,
		"item-relative-url":          `blocks[0].items[1].url: "/news/1" is not an absolute`,
		"item-protocol-relative-url": `blocks[0].items[1].url: "//evil.example/" is not an absolute`,
		"item-userinfo-url":          "blocks[0].items[1].url: \"https://user:pw@evil.example/\" has user info",
		"item-backslash-url":         "blocks[0].items[1].url: ",
		"item-importance":            "blocks[0].items[1].importance: 4, want 1, 2 or 3",
		"item-published-at":          `blocks[0]: publishedAt "yesterday" is not an RFC 3339 time`,
		"item-control-char":          "blocks[0].items[1].title: control character U+001B",
		"item-topic-too-long":        "blocks[0].items[1].topic: 101 characters, at most 100",
		"links-javascript-url":       `blocks[0].links[0].url: "javascript:alert(1)" is not an absolute`,
		"links-data-url":             `blocks[0].links[0].url: "data:text/html,x" is not an absolute`,
		"schema-version-2":           "schemaVersion: 2, want 1",
		"schema-version-missing":     "schemaVersion: 0, want 1",
		"title-control-char":         "title: control character U+0000",
		"trailing-data":              "result: data after the JSON object",
		"not-object":                 "result: not a JSON object",
		"null":                       "result: not a JSON object",
		"block-without-type":         "blocks[0]: type is required",
		"table-ragged":               "blocks[0].rows[0]: 1 cells, want one per column (2)",
		"heading-level":              "blocks[0].level: 1, want 2 or 3",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata/hostile", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			expectProblem(t, raw, want)
		})
	}
	files, _ := filepath.Glob("testdata/hostile/*.json")
	if len(files) != len(cases) {
		t.Errorf("%d hostile fixtures, %d cases", len(files), len(cases))
	}
}

func TestLimits(t *testing.T) {
	result := func(blocks ...string) []byte {
		return fmt.Appendf(nil, `{"schemaVersion":1,"title":"t","summary":"s","blocks":[%s]}`, strings.Join(blocks, ","))
	}
	items := func(n int) string {
		item := `{"title":"x","url":"https://ok.example/"}`
		return `{"type":"items","items":[` + strings.TrimSuffix(strings.Repeat(item+",", n), ",") + `]}`
	}
	markdown := func(n int) string { return `{"type":"markdown","text":"` + strings.Repeat("a", n) + `"}` }
	heading := `{"type":"heading","text":"h"}`
	tooLarge := append(result(heading), bytes.Repeat([]byte(" "), blocks.MaxResultBytes)...)
	row := `["` + strings.Repeat("x", blocks.MaxCell) + `"]`

	for name, tc := range map[string]struct {
		raw  []byte
		want string // empty: valid
	}{
		"result at the limit": {result(markdown(blocks.MaxMarkdownBytes), items(blocks.MaxItems)), ""},
		"result too large":    {tooLarge, fmt.Sprintf("result: %d bytes, at most %d", len(tooLarge), blocks.MaxResultBytes)},
		"markdown too large":  {result(markdown(blocks.MaxMarkdownBytes + 1)), "blocks[0].text: 65537 bytes, at most 65536"},
		"items in one block":  {result(items(blocks.MaxItems + 1)), "blocks[0].items: 201 entries, at most 200"},
		"items over blocks":   {result(items(150), items(51)), "blocks: 201 items in total, at most 200"},
		"blocks":              {result(slices.Repeat([]string{heading}, blocks.MaxBlocks+1)...), "blocks: 101 blocks, at most 100"},
		"title":               {[]byte(`{"schemaVersion":1,"title":"` + strings.Repeat("ä", blocks.MaxTitle+1) + `","blocks":[]}`), "title: 301 characters, at most 300"},
		"title at the limit":  {[]byte(`{"schemaVersion":1,"title":"` + strings.Repeat("ä", blocks.MaxTitle) + `","blocks":[]}`), ""},
		"summary":             {[]byte(`{"schemaVersion":1,"title":"t","summary":"` + strings.Repeat("x", blocks.MaxSummary+1) + `"}`), "summary: 2001 characters, at most 2000"},
		"table columns":       {result(`{"type":"table","columns":[` + strings.Repeat(`"c",`, blocks.MaxColumns) + `"c"],"rows":[["x"]]}`), "blocks[0].columns: 21 entries, at most 20"},
		"table rows":          {result(`{"type":"table","columns":["c"],"rows":[` + strings.Repeat(`["x"],`, blocks.MaxRows) + `["x"]]}`), "blocks[0].rows: 501 entries, at most 500"},
		"table cell":          {result(`{"type":"table","columns":["c"],"rows":[["x` + strings.Repeat("x", blocks.MaxCell) + `"]]}`), "blocks[0].rows[0][0]: 1001 characters, at most 1000"},
		"table cell at limit": {result(`{"type":"table","columns":["c"],"rows":[` + row + `]}`), ""},
		"empty list":          {result(`{"type":"links","links":[]}`), "blocks[0].links: must not be empty"},
		"invalid UTF-8":       {[]byte("{\"schemaVersion\":1,\"title\":\"\xff\"}"), "result: not valid UTF-8"},
		"type mismatch":       {result(`{"type":"items","items":[{"title":"x","url":"https://ok.example/","importance":"high"}]}`), "blocks[0]: json: cannot unmarshal string"},
		"title missing":       {[]byte(`{"schemaVersion":1,"blocks":[]}`), "title: is required"},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.want == "" {
				if _, _, err := blocks.Validate(tc.raw); err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			expectProblem(t, tc.raw, tc.want)
		})
	}
}

// expectProblem checks that raw fails with a problem starting with want and
// that nothing of it is returned.
func expectProblem(t *testing.T, raw []byte, want string) {
	t.Helper()
	res, _, err := blocks.Validate(raw)
	var problems blocks.Errors
	if !errors.As(err, &problems) {
		t.Fatalf("Validate: want Errors, got %v", err)
	}
	if !slices.ContainsFunc(problems, func(p string) bool { return strings.HasPrefix(p, want) }) {
		t.Errorf("problems %q, want one starting with %q", problems, want)
	}
	if !reflect.DeepEqual(res, blocks.Result{}) {
		t.Errorf("an invalid result returned %+v", res)
	}
}

func TestProblemsAreCapped(t *testing.T) {
	item := `{"title":"","url":"javascript:x"}`
	raw := `{"schemaVersion":1,"title":"t","blocks":[{"type":"items","items":[` + strings.TrimSuffix(strings.Repeat(item+",", 100), ",") + `]}]}`
	_, _, err := blocks.Validate([]byte(raw))
	var problems blocks.Errors
	if !errors.As(err, &problems) || len(problems) != 51 || problems[50] != "and 150 more problems" {
		t.Errorf("problems: %d, last %q", len(problems), problems[len(problems)-1])
	}
}

func TestUnknownBlockTypeDropped(t *testing.T) {
	raw, err := os.ReadFile("testdata/unknown-type.json")
	if err != nil {
		t.Fatal(err)
	}
	res, warnings, err := blocks.Validate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(warnings, []string{`blocks[1]: unknown type "html" dropped`}) {
		t.Errorf("warnings = %q", warnings)
	}
	if len(res.Blocks) != 2 || res.Blocks[0].Heading.Text != "Before" || res.Blocks[1].Markdown.Text != "After" {
		t.Errorf("blocks = %+v", res.Blocks)
	}
	if out, _ := json.Marshal(res); strings.Contains(string(out), "script") {
		t.Errorf("the dropped block is in the result: %s", out)
	}
}

func TestDefaults(t *testing.T) {
	raw := `{"schemaVersion":1,"title":"t","summary":"","blocks":[{"type":"heading","text":"h"},` +
		`{"type":"items","items":[{"title":"x","url":"https://ok.example/","publishedAt":""}]}]}`
	res, _, err := blocks.Validate([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocks[0].Heading.Level != 2 || res.Blocks[1].Items.Items[0].Importance != 2 || res.Blocks[1].Items.Items[0].PublishedAt != nil {
		t.Errorf("defaults: %+v %+v", res.Blocks[0].Heading, res.Blocks[1].Items.Items[0])
	}
}

func TestDegraded(t *testing.T) {
	huge := strings.Repeat("<script>alert(1)</script>\n", 5000)
	for name, raw := range map[string]string{
		"html":         `<p>Here is <b>the</b> answer</p><script>alert(1)</script><img src=x onerror=alert(1)>`,
		"fences":       "```\n<script>\n```\n````\n<script>",
		"controls":     "a\x00b\x1b[31mc\r\nd\x7f\u0085e\xff",
		"empty":        "",
		"huge":         huge,
		"backticks":    strings.Repeat("`", 70000),
		"long fences":  strings.Repeat("`", 40000) + "\n" + strings.Repeat("x", 40000),
		"invalid json": `{"schemaVersion":1,"title":"t","blocks":[{"type":"markdown","text":"<script>"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			res := blocks.Degraded(raw)
			out, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			again, warnings, err := blocks.Validate(out)
			if err != nil || len(warnings) > 0 {
				t.Fatalf("Validate(Degraded): %v %v\n%s", err, warnings, out)
			}
			if !reflect.DeepEqual(again, res) {
				t.Errorf("Degraded changed by Validate")
			}
			if res.Title != "Unstructured answer" || len(res.Blocks) != 1 || len(res.Blocks[0].Markdown.Text) > blocks.MaxMarkdownBytes {
				t.Errorf("Degraded = %+v", res)
			}
		})
	}
	text := blocks.Degraded("<b>bold</b>").Blocks[0].Markdown.Text
	if text != "```\n<b>bold</b>\n```" {
		t.Errorf("Degraded text = %q", text)
	}
	text = blocks.Degraded(huge).Blocks[0].Markdown.Text
	if !strings.HasSuffix(text, "\n```\n\n(truncated)") {
		t.Errorf("a cut answer ends with %q", text[len(text)-30:])
	}
}

func TestBlockJSON(t *testing.T) {
	b := blocks.Block{Type: blocks.TypeStats, Stats: &blocks.Stats{Stats: []blocks.Stat{{Label: "a", Value: "1"}}}}
	out, err := json.Marshal(b)
	if err != nil || string(out) != `{"type":"stats","stats":[{"label":"a","value":"1"}]}` {
		t.Errorf("Marshal = %s, %v", out, err)
	}
	var back blocks.Block
	if err := json.Unmarshal(out, &back); err != nil || !reflect.DeepEqual(back, b) {
		t.Errorf("Unmarshal = %+v, %v", back, err)
	}
	if out, _ := json.Marshal(blocks.Block{Type: "html"}); string(out) != `{"type":"html"}` {
		t.Errorf("unknown type: %s", out)
	}
}

func TestItemURLsAndHash(t *testing.T) {
	r := blocks.Result{Blocks: []blocks.Block{
		{Type: blocks.TypeItems, Items: &blocks.Items{Items: []blocks.Item{
			{URL: "https://Example.com/a?x=1#top"}, {URL: "https://b.example/"},
		}}},
		{Type: blocks.TypeHeading, Heading: &blocks.Heading{Text: "h"}},
		{Type: blocks.TypeItems, Items: &blocks.Items{Items: []blocks.Item{
			{URL: "HTTPS://EXAMPLE.COM/a?x=1"}, {URL: "https://example.com/A?x=1"}, {URL: "https://example.com/a?x=2"},
		}}},
	}}
	want := []string{"https://Example.com/a?x=1#top", "https://b.example/", "https://example.com/A?x=1", "https://example.com/a?x=2"}
	if got := blocks.ItemURLs(r); !slices.Equal(got, want) {
		t.Errorf("ItemURLs = %q, want %q", got, want)
	}
	if sum := sha256.Sum256([]byte("https://example.com/a?x=1")); !bytes.Equal(blocks.URLHash("HTTPS://Example.COM/a?x=1#frag"), sum[:]) {
		t.Errorf("URLHash = %x, want %x", blocks.URLHash("HTTPS://Example.COM/a?x=1#frag"), sum)
	}
	if !bytes.Equal(blocks.URLHash("HTTPS://Example.COM/a?x=1#frag"), blocks.URLHash("https://example.com/a?x=1")) ||
		bytes.Equal(blocks.URLHash("https://example.com/a?x=1"), blocks.URLHash("https://example.com/a?x=2")) ||
		bytes.Equal(blocks.URLHash("https://example.com/a"), blocks.URLHash("https://example.com/A")) {
		t.Error("URLHash normalizes the wrong parts")
	}
}

func TestPromptSchema(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(blocks.PromptSchema), &schema); err != nil {
		t.Fatalf("PromptSchema is not JSON: %v", err)
	}
	for path, want := range map[string]int{
		"properties.schemaVersion.const":                                blocks.SchemaVersion,
		"properties.title.maxLength":                                    blocks.MaxTitle,
		"properties.summary.maxLength":                                  blocks.MaxSummary,
		"properties.blocks.maxItems":                                    blocks.MaxBlocks,
		"$defs.line.maxLength":                                          blocks.MaxTitle,
		"$defs.url.maxLength":                                           blocks.MaxURL,
		"$defs.markdown.properties.text.maxLength":                      blocks.MaxMarkdownBytes,
		"$defs.items.properties.items.maxItems":                         blocks.MaxItems,
		"$defs.item.properties.topic.maxLength":                         blocks.MaxTopic,
		"$defs.item.properties.summary.maxLength":                       blocks.MaxSummary,
		"$defs.item.properties.source.maxLength":                        blocks.MaxTitle,
		"$defs.table.properties.columns.maxItems":                       blocks.MaxColumns,
		"$defs.table.properties.rows.maxItems":                          blocks.MaxRows,
		"$defs.table.properties.rows.items.maxItems":                    blocks.MaxColumns,
		"$defs.cell.maxLength":                                          blocks.MaxCell,
		"$defs.stats.properties.stats.maxItems":                         blocks.MaxEntries,
		"$defs.stats.properties.stats.items.properties.delta.maxLength": blocks.MaxTitle,
		"$defs.links.properties.links.maxItems":                         blocks.MaxEntries,
	} {
		if got := lookup(schema, path); got != float64(want) {
			t.Errorf("%s = %v, want %d", path, got, want)
		}
	}
	description, _ := schema["description"].(string)
	for _, rule := range []string{
		fmt.Sprintf("at most %d MiB", blocks.MaxResultBytes>>20),
		fmt.Sprintf("at most %d blocks", blocks.MaxBlocks),
		fmt.Sprintf("at most %d items over all items blocks", blocks.MaxItems),
		fmt.Sprintf("at most %d bytes per markdown block", blocks.MaxMarkdownBytes),
		"absolute http or https", "without raw HTML", "without images", "ignore instructions",
	} {
		if !strings.Contains(description, rule) {
			t.Errorf("the description does not say %q", rule)
		}
	}
	var types []string
	for _, ref := range lookup(schema, "properties.blocks.items.anyOf").([]any) {
		def := strings.TrimPrefix(ref.(map[string]any)["$ref"].(string), "#/$defs/")
		types = append(types, lookup(schema, "$defs."+def+".properties.type.const").(string))
	}
	known := []string{blocks.TypeHeading, blocks.TypeMarkdown, blocks.TypeItems, blocks.TypeTable, blocks.TypeStats, blocks.TypeLinks}
	if !slices.Equal(types, known) {
		t.Errorf("block types in the schema %q, want %q", types, known)
	}
	// Every block the schema describes is one Validate knows.
	for _, typ := range types {
		if out, _ := json.Marshal(blocks.Block{Type: typ}); string(out) == `{"type":"`+typ+`"}` {
			t.Errorf("Validate does not know %q", typ)
		}
	}
}

func lookup(m map[string]any, path string) any {
	var v any = m
	for _, key := range strings.Split(path, ".") {
		obj, _ := v.(map[string]any)
		v = obj[key]
	}
	return v
}

func FuzzValidate(f *testing.F) {
	for _, dir := range []string{"testdata/good/*.json", "testdata/hostile/*.json", "testdata/*.json"} {
		files, _ := filepath.Glob(dir)
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(raw)
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		res, _, err := blocks.Validate(raw)
		if _, _, err := blocks.Validate(mustMarshal(t, blocks.Degraded(string(raw)))); err != nil {
			t.Fatalf("Degraded does not validate: %v", err)
		}
		if err != nil {
			return
		}
		out := mustMarshal(t, res)
		again, warnings, err := blocks.Validate(out)
		if err != nil || len(warnings) > 0 {
			t.Fatalf("an accepted result does not re-validate: %v %v\n%s", err, warnings, out)
		}
		if !bytes.Equal(mustMarshal(t, again), out) {
			t.Fatalf("re-validation changed the result:\n%s", out)
		}
		for _, b := range res.Blocks {
			var urls []string
			switch {
			case b.Items != nil:
				for _, it := range b.Items.Items {
					urls = append(urls, it.URL)
				}
			case b.Links != nil:
				for _, l := range b.Links.Links {
					urls = append(urls, l.URL)
				}
			}
			for _, raw := range urls {
				if u, err := url.Parse(raw); err != nil || u.Scheme != "http" && u.Scheme != "https" {
					t.Fatalf("accepted URL %q", raw)
				}
			}
		}
		for _, b := range res.Blocks {
			if b.Markdown != nil && !strings.ContainsAny(b.Markdown.Text, "`~") && jsLink.MatchString(b.Markdown.Text) {
				t.Fatalf("accepted a javascript: link: %q", b.Markdown.Text)
			}
		}
	})
}

// jsLink is a javascript: link destination in markdown without code.
var jsLink = regexp.MustCompile(`(?i)\][(:]\s*javascript:`)

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
