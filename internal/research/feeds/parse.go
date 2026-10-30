package feeds

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// Entry is one RSS item or Atom entry after plain-text normalisation.
type Entry struct {
	Title, URL, Summary string
	Published           time.Time // zero when the feed has no date
}

// aLink is an Atom link element.
type aLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

// Parse detects RSS 2.0 or Atom 1.0 and returns its entries.
func Parse(r io.Reader) ([]Entry, error) {
	dec := xml.NewDecoder(r)
	dec.Strict, dec.CharsetReader = false, charsetReader
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "rss":
			var d struct {
				Ch struct {
					Link string `xml:"link"`
					Item []struct {
						Title       string `xml:"title"`
						Link        string `xml:"link"`
						Description string `xml:"description"`
						PubDate     string `xml:"pubDate"`
						Encoded     string `xml:"encoded"`
						GUID        struct {
							V string `xml:",chardata"`
							P string `xml:"isPermaLink,attr"`
						} `xml:"guid"`
					} `xml:"item"`
				} `xml:"channel"`
			}
			if err := dec.DecodeElement(&d, &se); err != nil {
				return nil, err
			}
			out := make([]Entry, 0, len(d.Ch.Item))
			for _, it := range d.Ch.Item {
				link, sum := it.Link, it.Description
				if link == "" && it.GUID.P != "false" {
					link = it.GUID.V
				}
				if sum == "" {
					sum = it.Encoded
				}
				out = append(out, Entry{plainText(it.Title, MaxTitle), resolve(d.Ch.Link, strings.TrimSpace(link)), plainText(sum, MaxSummary), parseDate(it.PubDate)})
			}
			return out, nil
		case "feed":
			var d struct {
				Link  []aLink `xml:"link"`
				Entry []struct {
					Title     string  `xml:"title"`
					Published string  `xml:"published"`
					Updated   string  `xml:"updated"`
					Summary   string  `xml:"summary"`
					Content   string  `xml:"content"`
					Link      []aLink `xml:"link"`
				} `xml:"entry"`
			}
			if err := dec.DecodeElement(&d, &se); err != nil {
				return nil, err
			}
			base, out := href(d.Link), make([]Entry, 0, len(d.Entry))
			for _, e := range d.Entry {
				sum, pub := e.Summary, e.Published
				if sum == "" {
					sum = e.Content
				}
				if pub == "" {
					pub = e.Updated
				}
				out = append(out, Entry{plainText(e.Title, MaxTitle), resolve(base, href(e.Link)), plainText(sum, MaxSummary), parseDate(pub)})
			}
			return out, nil
		default:
			return nil, fmt.Errorf("feeds: unknown root element %q", se.Name.Local)
		}
	}
}

// href picks an Atom entry's alternate link, else its first link.
func href(ls []aLink) string {
	var fb string
	for _, l := range ls {
		if l.Href == "" {
			continue
		}
		if l.Rel == "alternate" || l.Rel == "" {
			return l.Href
		}
		if fb == "" {
			fb = l.Href
		}
	}
	return fb
}

// resolve makes a relative link absolute against the feed's own link.
func resolve(base, ref string) string {
	u, err := url.Parse(ref)
	if ref == "" || err != nil || u.IsAbs() || base == "" {
		return ref
	}
	if b, err := url.Parse(base); err == nil {
		return b.ResolveReference(u).String()
	}
	return ref
}

// parseDate reads RFC 822/1123 (also with a one-digit day) and RFC 3339
// dates; anything else is the zero time.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
		if t, err := time.Parse(strings.Replace(l, "02", "2", 1), s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// charsetReader decodes ISO-8859-1 feeds; UTF-8 and ASCII pass through.
func charsetReader(cs string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(cs) {
	case "utf-8", "us-ascii", "":
		return in, nil
	case "iso-8859-1", "latin1":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return strings.NewReader(string(r)), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", cs)
}
