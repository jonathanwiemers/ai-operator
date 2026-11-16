package docscheck

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
)

// pageCap is the most lines a capped page may have (milestone decision 4).
const pageCap = 400

// userFolders are the four sections written for users. The page cap and the
// source-ref rule apply to them.
var userFolders = []string{
	"docs/getting-started/",
	"docs/guides/",
	"docs/reference/",
	"docs/concepts/",
}

// markdownFiles are the Markdown files outside docs/ and skills/ that are
// checked.
var markdownFiles = map[string]bool{
	"README.md":                    true,
	"CONTRIBUTING.md":              true,
	"SECURITY.md":                  true,
	"CLAUDE.md":                    true,
	"charts/ai-operator/README.md": true,
}

// entryPoints are linked from outside the repository (the launch blog post)
// and must keep resolving.
var entryPoints = []string{
	"docs/README.md",
	"README.md#quick-start",
}

// skipDirNames are directories skipped wherever they are; skipDirPaths are
// skipped at exactly this path. Every other directory whose name starts with
// a dot is skipped too, except keepDotDirs: CI keeps its Go and npm caches in
// .cache inside the checkout, megabytes of files that are not ours.
var (
	skipDirNames = map[string]bool{
		".git": true, "node_modules": true, "dist": true, "bin": true,
		"vendor": true, "testdata": true,
	}
	keepDotDirs  = map[string]bool{".github": true, ".gitlab": true, ".claude": true}
	skipDirPaths = map[string]bool{
		"web/e2e-results": true, "web/test-results": true,
	}
	// markdownSkipDirs hold Markdown with its own context (a sample
	// repository), which is not part of these docs.
	markdownSkipDirs = []string{"hack/sandbox/"}
	// binaryExts are images (SVG included) and other binary files, which
	// are not scanned for docs refs.
	binaryExts = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".webp": true, ".svg": true, ".ico": true, ".avif": true, ".bmp": true,
		".woff": true, ".woff2": true, ".ttf": true, ".pdf": true,
	}
)

var (
	// headingRE is an ATX heading, also inside a blockquote.
	headingRE = regexp.MustCompile(`^(?: {0,3}>[ \t]?)* {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)
	closingRE = regexp.MustCompile(`(?:^|[ \t]+)#+$`)
	// anchorTagRE is an explicit anchor: id or name on <a>, id on <h1>–<h6>.
	anchorTagRE = regexp.MustCompile(`<(?:a\s(?:[^>]*\s)?(?:id|name)|h[1-6]\s(?:[^>]*\s)?id)\s*=\s*["']([^"']+)["']`)
	// inlineLinkRE matches the target of [text](target) and ![alt](src).
	inlineLinkRE = regexp.MustCompile(`\]\(\s*(<[^>]*>|[^)\s]+)`)
	refDefRE     = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)
	htmlAttrRE   = regexp.MustCompile(`\b(?:href|src)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	schemeRE     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

	imageRE    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	linkTextRE = regexp.MustCompile(`\[([^\]]*)\](?:\([^)]*\)|\[[^\]]*\])`)
	htmlTagRE  = regexp.MustCompile(`</?[A-Za-z][^>]*>`)
	escapeRE   = regexp.MustCompile(`\\([[:punct:]])`)

	// docsRefRE finds docs refs in non-Markdown files. Group 1 is the
	// path, group 2 the anchor.
	docsRefRE = regexp.MustCompile(`(?:^|[^\w.-])(docs/[\w./-]+\.md)(?:#([\w-]+))?`)

	urlRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S+`)
	// allowedTargetRE is a link target to a page or an image, which a user
	// page may name.
	allowedTargetRE = regexp.MustCompile(`(?i)\]\(\s*<?[^)\s>]*\.(?:md|png|jpe?g|gif|webp|svg|avif)(?:#[^)\s>]*)?>?`)
	srcFileRE       = regexp.MustCompile(`(?:^|[^\w./-])([\w./-]*[\w-]\.(?:go|ts|vue|js|mjs|sql|sh)(?::\d+)?)\b`)
	srcDirRE        = regexp.MustCompile(`(?:^|[^\w./-])((?:internal|api|cmd|web|hack|config|test)/[\w./-]*)`)
	// productRE is a product name like Node.js or Vue.js, not a file.
	productRE = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*\.js$`)
)

// finding is one problem, reported as path:line: message.
type finding struct {
	path string
	line int
	msg  string
}

func (f finding) String() string { return fmt.Sprintf("%s:%d: %s", f.path, f.line, f.msg) }

// checker checks files of one tree. fsys resolves link targets and reads the
// pages they point to.
type checker struct {
	fsys    fs.FS
	anchors map[string]map[string]bool
}

func newChecker(fsys fs.FS) *checker {
	return &checker{fsys: fsys, anchors: map[string]map[string]bool{}}
}

// mdLine is one line of a Markdown page.
type mdLine struct {
	n    int    // 1-based line number
	text string // the line, without the line break
	code bool   // inside a fenced code block, fence lines included
}

// scanLines splits a Markdown page into lines and marks fenced code.
func scanLines(content string) []mdLine {
	var out []mdLine
	var fenceChar byte
	fenceLen := 0
	for i, l := range strings.Split(content, "\n") {
		l = strings.TrimSuffix(l, "\r")
		t := strings.TrimLeft(l, " \t")
		c, n := fence(t)
		switch {
		case fenceLen == 0 && n > 0:
			fenceChar, fenceLen = c, n
			out = append(out, mdLine{i + 1, l, true})
		case fenceLen > 0:
			if c == fenceChar && n >= fenceLen && strings.TrimSpace(t[n:]) == "" {
				fenceLen = 0
			}
			out = append(out, mdLine{i + 1, l, true})
		default:
			out = append(out, mdLine{i + 1, l, false})
		}
	}
	return out
}

// fence returns the fence character and length if t opens or closes a code
// fence.
func fence(t string) (byte, int) {
	if t == "" || (t[0] != '`' && t[0] != '~') {
		return 0, 0
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	if n < 3 || (t[0] == '`' && strings.Contains(t[n:], "`")) {
		return 0, 0
	}
	return t[0], n
}

// segment is a piece of a line, inline code or not.
type segment struct {
	text string
	code bool
}

// splitCode splits a line into inline code spans and the text around them.
func splitCode(s string) []segment {
	var out []segment
	start := 0
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		n := runLen(s, i)
		end := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := runLen(s, j)
			if m == n {
				end = j
				break
			}
			j += m
		}
		if end < 0 {
			i += n
			continue
		}
		if start < i {
			out = append(out, segment{s[start:i], false})
		}
		code := s[i+n : end]
		if len(code) >= 2 && code[0] == ' ' && code[len(code)-1] == ' ' && strings.Trim(code, " ") != "" {
			code = code[1 : len(code)-1]
		}
		out = append(out, segment{code, true})
		i = end + n
		start = i
	}
	if start < len(s) {
		out = append(out, segment{s[start:], false})
	}
	return out
}

func runLen(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == s[i] {
		n++
	}
	return n
}

// withoutCode returns the line with its inline code spans blanked out.
func withoutCode(s string) string {
	var b strings.Builder
	for _, seg := range splitCode(s) {
		if seg.code {
			b.WriteString(" ")
		} else {
			b.WriteString(seg.text)
		}
	}
	return b.String()
}

// headingText returns the rendered text of an ATX heading's raw text:
// inline code without backticks, link and image text without the target,
// no HTML tags and no backslash escapes.
func headingText(raw string) string {
	var b strings.Builder
	for _, seg := range splitCode(raw) {
		if seg.code {
			b.WriteString(seg.text)
			continue
		}
		t := imageRE.ReplaceAllString(seg.text, "$1")
		t = linkTextRE.ReplaceAllString(t, "$1")
		t = htmlTagRE.ReplaceAllString(t, "")
		t = escapeRE.ReplaceAllString(t, "$1")
		b.WriteString(t)
	}
	return strings.TrimSpace(b.String())
}

// slug is GitHub's heading slug: lowercase, keep letters, digits, spaces,
// `-` and `_`, then turn each space into `-`.
func slug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// anchorsOf returns the anchors of a page: heading slugs, numbered -1, -2, …
// when they repeat (blockquoted headings included), and explicit ids:
// <a id>, <a name> and <h1 id> to <h6 id>.
func anchorsOf(content string) map[string]bool {
	set := map[string]bool{}
	seen := map[string]int{}
	for _, l := range scanLines(content) {
		if l.code {
			continue
		}
		if m := headingRE.FindStringSubmatch(l.text); m != nil {
			raw := closingRE.ReplaceAllString(m[2], "")
			s := slug(headingText(raw))
			id := s
			for {
				if _, dup := seen[id]; !dup {
					break
				}
				seen[s]++
				id = fmt.Sprintf("%s-%d", s, seen[s])
			}
			seen[id] = 0
			set[id] = true
		}
		for _, m := range anchorTagRE.FindAllStringSubmatch(withoutCode(l.text), -1) {
			set[m[1]] = true
		}
	}
	return set
}

// pageAnchors returns the anchors of a page in the tree, read once.
func (c *checker) pageAnchors(p string) map[string]bool {
	if a, ok := c.anchors[p]; ok {
		return a
	}
	data, err := fs.ReadFile(c.fsys, p)
	a := map[string]bool{}
	if err == nil {
		a = anchorsOf(string(data))
	}
	c.anchors[p] = a
	return a
}

// linkTargets returns the link targets of a Markdown line outside code:
// inline links and images, reference definitions and HTML href and src.
func linkTargets(line string) []string {
	s := withoutCode(line)
	var out []string
	for _, m := range inlineLinkRE.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.Trim(m[1], "<>"))
	}
	if m := refDefRE.FindStringSubmatch(s); m != nil {
		out = append(out, strings.Trim(m[1], "<>"))
	}
	for _, m := range htmlAttrRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1]+m[2])
	}
	return out
}

// checkTarget resolves a link target of the page from and returns what is
// wrong with it, or "".
func (c *checker) checkTarget(from, target string) string {
	if target == "" || strings.HasPrefix(target, "//") || schemeRE.MatchString(target) {
		return ""
	}
	p, frag, _ := strings.Cut(target, "#")
	p, _, _ = strings.Cut(p, "?")
	p, err := url.PathUnescape(p)
	if err != nil {
		return err.Error()
	}
	if f, err := url.PathUnescape(frag); err == nil {
		frag = f
	}
	resolved := from
	switch {
	case strings.HasPrefix(p, "/"):
		resolved = path.Clean(strings.TrimLeft(p, "/"))
	case p != "":
		resolved = path.Join(path.Dir(from), p)
	}
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "it points outside the repository"
	}
	isDir := false
	if p != "" {
		info, err := fs.Stat(c.fsys, resolved)
		if err != nil {
			return resolved + " does not exist"
		}
		isDir = info.IsDir()
	}
	if frag == "" || isDir || !strings.HasSuffix(resolved, ".md") {
		return ""
	}
	if !c.pageAnchors(resolved)[frag] {
		return fmt.Sprintf("no heading or anchor #%s in %s", frag, resolved)
	}
	return ""
}

// markdown checks one Markdown page: links and anchors, and for capped pages
// and user pages the page cap and source refs.
func (c *checker) markdown(p, content string) []finding {
	c.anchors[p] = anchorsOf(content)
	var out []finding
	lines := scanLines(content)
	user := inUserFolder(p)
	for _, l := range lines {
		if !l.code {
			for _, t := range linkTargets(l.text) {
				if msg := c.checkTarget(p, t); msg != "" {
					out = append(out, finding{p, l.n, fmt.Sprintf("link %s: %s", t, msg)})
				}
			}
		}
		if user {
			for _, ref := range sourceRefs(l.text) {
				out = append(out, finding{p, l.n, fmt.Sprintf(
					"source ref %q in a user page: name no source files or directories, internals go to docs/contributing/", ref)})
			}
		}
	}
	if capped(p) {
		if n := lineCount(content); n > pageCap {
			out = append(out, finding{p, pageCap + 1, fmt.Sprintf(
				"page has %d lines, the cap is %d: split it into several pages", n, pageCap)})
		}
	}
	return out
}

// sourceRefs returns the source file paths and source directory paths named
// on a line of a user page. URLs and link targets to pages and images pass.
func sourceRefs(line string) []string {
	s := allowedTargetRE.ReplaceAllString(line, "](")
	s = urlRE.ReplaceAllString(s, " ")
	var out []string
	seen := map[string]bool{}
	add := func(ref string) {
		ref = strings.TrimRight(ref, ".")
		if ref == "" || seen[ref] || productRE.MatchString(ref) {
			return
		}
		seen[ref] = true
		out = append(out, ref)
	}
	for _, m := range srcFileRE.FindAllStringSubmatch(s, -1) {
		add(m[1])
	}
	for _, m := range srcDirRE.FindAllStringSubmatch(s, -1) {
		ref := strings.TrimRight(m[1], ".")
		covered := false
		for r := range seen {
			if strings.HasPrefix(r, ref) {
				covered = true
			}
		}
		if !covered {
			add(ref)
		}
	}
	return out
}

func inUserFolder(p string) bool {
	for _, f := range userFolders {
		if strings.HasPrefix(p, f) {
			return true
		}
	}
	return false
}

// capped reports whether the page cap applies to p: user pages and every
// README under docs/.
func capped(p string) bool {
	return inUserFolder(p) || (strings.HasPrefix(p, "docs/") && path.Base(p) == "README.md")
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// nonMarkdown checks the docs refs of a non-Markdown file: the page must
// exist, and an anchor must resolve.
func (c *checker) nonMarkdown(p, content string) []finding {
	if !strings.Contains(content, "docs/") {
		return nil
	}
	var out []finding
	for _, m := range docsRefRE.FindAllStringSubmatchIndex(content, -1) {
		page := content[m[2]:m[3]]
		ref := page
		if m[4] >= 0 {
			ref += "#" + content[m[4]:m[5]]
		}
		line := strings.Count(content[:m[2]], "\n") + 1
		if msg := c.checkTarget("", "/"+ref); msg != "" {
			out = append(out, finding{p, line, fmt.Sprintf("docs ref %s: %s", ref, msg)})
		}
	}
	return out
}

// entryPointFindings checks the refs linked from outside the repository.
func (c *checker) entryPointFindings(refs []string) []finding {
	var out []finding
	for _, ref := range refs {
		if msg := c.checkTarget("", "/"+ref); msg != "" {
			page, _, _ := strings.Cut(ref, "#")
			out = append(out, finding{page, 1, fmt.Sprintf(
				"entry point %s: %s (linked from outside the repository, keep it working)", ref, msg)})
		}
	}
	return out
}

// isCheckedMarkdown reports whether the Markdown file p is checked.
func isCheckedMarkdown(p string) bool {
	if !strings.HasSuffix(p, ".md") {
		return false
	}
	for _, d := range markdownSkipDirs {
		if strings.HasPrefix(p, d) {
			return false
		}
	}
	return markdownFiles[p] || strings.HasPrefix(p, "docs/") || strings.HasPrefix(p, "skills/")
}

func isBinary(p string, data []byte) bool {
	return binaryExts[strings.ToLower(path.Ext(p))] || bytes.IndexByte(data, 0) >= 0
}

// checkTree checks every file of the tree and returns all findings, sorted.
func checkTree(fsys fs.FS) ([]finding, error) {
	c := newChecker(fsys)
	var out []finding
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			hidden := strings.HasPrefix(name, ".") && !keepDotDirs[name]
			if p != "." && (hidden || skipDirNames[name] || skipDirPaths[p]) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		isMD := strings.HasSuffix(p, ".md")
		if isMD && !isCheckedMarkdown(p) {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		switch {
		case isMD:
			out = append(out, c.markdown(p, string(data))...)
		case !isBinary(p, data):
			out = append(out, c.nonMarkdown(p, string(data))...)
		}
		return nil
	})
	out = append(out, c.entryPointFindings(entryPoints)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].line < out[j].line
	})
	return out, err
}

// repoRoot walks up from the working directory to the directory with go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// TestDocs runs the docs check against the repository.
func TestDocs(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	findings, err := checkTree(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		return
	}
	lines := make([]string, len(findings))
	for i, f := range findings {
		lines[i] = f.String()
	}
	t.Errorf("docs check: %d findings (see internal/docscheck/doc.go):\n%s", len(findings), strings.Join(lines, "\n"))
}
