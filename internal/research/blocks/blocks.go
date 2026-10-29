// Package blocks is the result format research agents return (block schema
// v1) and the validator that stands between the agent and the database and
// browser. Agent output is untrusted web content: only a Result returned by
// Validate (or Degraded) may be stored or rendered.
package blocks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Block types of schema v1.
const (
	TypeHeading  = "heading"
	TypeMarkdown = "markdown"
	TypeItems    = "items"
	TypeTable    = "table"
	TypeStats    = "stats"
	TypeLinks    = "links"
)

// Limits of schema v1, also stated in PromptSchema. Lengths are characters
// unless they say bytes.
const (
	SchemaVersion    = 1
	MaxResultBytes   = 1 << 20
	MaxBlocks        = 100
	MaxItems         = 200 // per result, over all items blocks
	MaxEntries       = 200 // per stats or links block
	MaxMarkdownBytes = 64 << 10
	MaxTitle         = 300  // also headings, item titles, sources, link titles, stats
	MaxSummary       = 2000 // also item summaries
	MaxTopic         = 100
	MaxURL           = 2048
	MaxColumns       = 20
	MaxRows          = 500
	MaxCell          = 1000 // also column names
)

// Result is an agent's answer.
type Result struct {
	SchemaVersion int     `json:"schemaVersion"`
	Title         string  `json:"title"`
	Summary       string  `json:"summary"`
	Blocks        []Block `json:"blocks"`
}

// Block is one block of a result: Type says which field is set. In JSON it
// is a flat object, {"type":"items","items":[…]}.
type Block struct {
	Type     string
	Heading  *Heading
	Markdown *Markdown
	Items    *Items
	Table    *Table
	Stats    *Stats
	Links    *Links
}

// Heading is a section heading.
type Heading struct {
	Text  string `json:"text"`
	Level int    `json:"level"` // 2 or 3
}

// Markdown is CommonMark without raw HTML and images.
type Markdown struct {
	Text string `json:"text"`
}

// Items are news or findings.
type Items struct {
	Items []Item `json:"items"`
}

// Item is one piece of news or one finding.
type Item struct {
	Topic       string     `json:"topic,omitempty"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary,omitempty"`
	URL         string     `json:"url"`
	Source      string     `json:"source,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	Importance  int        `json:"importance"` // 1 (low) to 3 (high)
}

// Table is a table of strings; every row has one cell per column.
type Table struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Stats are key figures.
type Stats struct {
	Stats []Stat `json:"stats"`
}

// Stat is one key figure, with an optional change ("+3 %").
type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Delta string `json:"delta,omitempty"`
}

// Links are a list of links.
type Links struct {
	Links []Link `json:"links"`
}

// Link is one link.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// body returns the field of b's type, allocated if nil; nil for an unknown type.
func (b *Block) body() any {
	switch b.Type {
	case TypeHeading:
		return alloc(&b.Heading)
	case TypeMarkdown:
		return alloc(&b.Markdown)
	case TypeItems:
		return alloc(&b.Items)
	case TypeTable:
		return alloc(&b.Table)
	case TypeStats:
		return alloc(&b.Stats)
	case TypeLinks:
		return alloc(&b.Links)
	}
	return nil
}

func alloc[T any](p **T) *T {
	if *p == nil {
		*p = new(T)
	}
	return *p
}

// MarshalJSON writes the type and the fields of its body into one object.
func (b Block) MarshalJSON() ([]byte, error) {
	typ, err := json.Marshal(b.Type)
	if err != nil {
		return nil, err
	}
	out := append([]byte(`{"type":`), typ...)
	if body := b.body(); body != nil {
		fields, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if len(fields) > 2 {
			out = append(append(out, ','), fields[1:len(fields)-1]...)
		}
	}
	return append(out, '}'), nil
}

// UnmarshalJSON reads a flat block object. An unknown type leaves every body
// nil; Validate drops such blocks.
func (b *Block) UnmarshalJSON(data []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	*b = Block{Type: head.Type}
	if body := b.body(); body != nil {
		return json.Unmarshal(data, body)
	}
	return nil
}

// UnmarshalJSON accepts an empty publishedAt as unset.
func (it *Item) UnmarshalJSON(data []byte) error {
	type plain Item
	var w struct {
		plain
		PublishedAt string `json:"publishedAt"`
	}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*it = Item(w.plain)
	if w.PublishedAt != "" {
		t, err := time.Parse(time.RFC3339, w.PublishedAt)
		if err != nil {
			return fmt.Errorf("publishedAt %.40q is not an RFC 3339 time", w.PublishedAt)
		}
		it.PublishedAt = &t
	}
	return nil
}

// ItemURLs returns the URLs of r's items in order, without duplicates by
// URLHash.
func ItemURLs(r Result) []string {
	var urls []string
	seen := map[string]bool{}
	for _, b := range r.Blocks {
		if b.Items == nil {
			continue
		}
		for _, it := range b.Items.Items {
			if h := string(URLHash(it.URL)); !seen[h] {
				seen[h] = true
				urls = append(urls, it.URL)
			}
		}
	}
	return urls
}

// URLHash identifies a URL for dedupe and "new" badges: the sha256 of the
// URL with scheme and host lowercased and without fragment. The query is kept.
func URLHash(raw string) []byte {
	if u, err := url.Parse(raw); err == nil {
		u.Scheme, u.Host = strings.ToLower(u.Scheme), strings.ToLower(u.Host)
		u.Fragment, u.RawFragment = "", ""
		raw = u.String()
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
