package attachments

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Project names the GitLab project whose uploads count as attachments.
type Project struct {
	// URL is the GitLab base URL, for example "https://gitlab.com".
	URL string
	// Path is the project's path with namespace, for example "group/app".
	Path string
	// ID is the project's numeric ID, 0 when unknown: then links of the
	// /-/project/<id>/ form do not count.
	ID int64
}

// Link is an upload link found in text.
type Link struct {
	// Raw is the URL exactly as written in the text.
	Raw string
	// Secret is the upload's 32 hex characters, in lower case.
	Secret string
	// Filename is the upload's file name, URL-decoded: one path segment.
	Filename string
	// Image is set when the link is written as a markdown image, ![..](..).
	Image bool
}

// linkRE matches a markdown link or image with an optional title (groups 1
// and 2: the "!" and the URL), or a bare http(s) URL.
var linkRE = regexp.MustCompile(`(!?)\[[^\]\n]*\]\(\s*([^\s()]+)(?:\s+"[^"\n]*")?\s*\)|https?://[^\s<>()\[\]"'` + "`" + `]+`)

// Find returns the upload links of project p in text, in order of
// appearance, each secret and file name once. Markdown links and images
// count, and so do bare URLs. A link counts only in one of three forms:
//
//	/uploads/<secret>/<file>
//	<p.URL>/<p.Path>/uploads/<secret>/<file>
//	<p.URL>/-/project/<p.ID>/uploads/<secret>/<file>   (only when p.ID != 0)
//
// The relative form counts in markdown links only. <secret> is 32 hex
// characters, and <file> is one URL-encoded path segment that does not
// decode to "." or "..", or to a name with / or \. Host and path compare
// case-insensitively. Anything else, including a query or a fragment, is
// ignored.
func Find(text string, p Project) []Link {
	prefixes := p.prefixes()
	var out []Link
	seen := map[string]bool{}
	for _, m := range linkRE.FindAllStringSubmatchIndex(text, -1) {
		var l Link
		if m[4] >= 0 {
			l.Raw, l.Image = text[m[4]:m[5]], m[3] > m[2]
		} else {
			l.Raw = strings.TrimRight(text[m[0]:m[1]], ".,;:!?")
		}
		var ok bool
		if l.Secret, l.Filename, ok = parse(l.Raw, prefixes); !ok {
			continue
		}
		if key := l.Secret + "/" + l.Filename; !seen[key] {
			seen[key] = true
			out = append(out, l)
		}
	}
	return out
}

// prefixes returns the URL prefixes an upload path follows in p.
func (p Project) prefixes() []string {
	out := []string{"/uploads/"}
	base := strings.TrimRight(p.URL, "/")
	if base == "" {
		return out
	}
	if path := strings.Trim(p.Path, "/"); path != "" {
		out = append(out, base+"/"+path+"/uploads/")
	}
	if p.ID != 0 {
		out = append(out, base+"/-/project/"+strconv.FormatInt(p.ID, 10)+"/uploads/")
	}
	return out
}

// parse returns the secret and the decoded file name of raw when it is an
// upload link under one of prefixes.
func parse(raw string, prefixes []string) (secret, file string, ok bool) {
	rest := ""
	for _, pre := range prefixes {
		if len(raw) > len(pre) && strings.EqualFold(raw[:len(pre)], pre) {
			rest = raw[len(pre):]
			break
		}
	}
	secret, file, ok = strings.Cut(rest, "/")
	if !ok || !isSecret(secret) || strings.ContainsAny(file, "/?#") {
		return "", "", false
	}
	file, err := url.PathUnescape(file)
	if err != nil || !segment(file) {
		return "", "", false
	}
	return strings.ToLower(secret), file, true
}

// isSecret reports whether s is 32 hex characters.
func isSecret(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// segment reports whether a decoded file name is one harmless path segment.
func segment(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}
