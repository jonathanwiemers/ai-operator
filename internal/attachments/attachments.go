// Package attachments handles files attached to text as GitLab project
// uploads: screenshots, PDFs and plain text files linked in a Desk plan's
// chat, a draft, and later in milestones, issues and comments. It is shared
// by the Desk server (the upload and file routes) and the runner (the plan
// turn), so the allowed types, the limits and the rules for which links count
// live in one place. It imports the standard library only.
//
// Allowed checks a file's extension and sniffed content. Find picks the
// upload links of one project out of untrusted text, so the text cannot make
// a caller fetch anything else. Fetch downloads such links into a directory
// under caps. Fetch takes the project (its path with namespace, or its ID)
// as a parameter rather than in each Link, because Find returns links of one
// project only.
package attachments

import (
	"net/http"
	"path/filepath"
	"strings"
)

// Limits on attachments.
const (
	// MaxFileBytes is the size limit of one file.
	MaxFileBytes = 10 << 20
	// MaxFiles is the number of files one message may carry.
	MaxFiles = 5
	// MaxTurnFiles is the number of files Fetch takes for one agent turn.
	MaxTurnFiles = 20
	// MaxTurnBytes is the total size of the files Fetch takes for one turn.
	MaxTurnBytes = 50 << 20
)

// Extensions are the allowed file extensions, lower case, in a fixed order.
var Extensions = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf",
	".txt", ".log", ".md", ".json", ".yaml", ".yml", ".csv"}

const textPlain = "text/plain"

// sniffed maps each of Extensions to the type http.DetectContentType must
// report for its content.
var sniffed = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".pdf": "application/pdf",
	".txt": textPlain, ".log": textPlain, ".md": textPlain, ".json": textPlain,
	".yaml": textPlain, ".yml": textPlain, ".csv": textPlain,
}

// allowedExt reports whether name has one of Extensions, in any case.
func allowedExt(name string) bool {
	_, ok := sniffed[strings.ToLower(filepath.Ext(name))]
	return ok
}

// Allowed reports whether a file named name, whose content starts with head,
// is an allowed attachment, and the content type to serve it with. The
// extension must be one of Extensions, and http.DetectContentType(head) must
// agree with it: an image or a PDF must sniff as its own type, which is
// returned; a text file must sniff as text/plain in any charset, and is
// served as "text/plain; charset=utf-8".
func Allowed(name string, head []byte) (contentType string, ok bool) {
	want, ok := sniffed[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return "", false
	}
	got := http.DetectContentType(head)
	if want == textPlain {
		if mediaType(got) == textPlain {
			return textPlain + "; charset=utf-8", true
		}
		return "", false
	}
	if got != want {
		return "", false
	}
	return got, true
}

// kinds names the content each sniffed type stands for, in a refusal.
var kinds = map[string]string{
	"image/png": "a PNG image", "image/jpeg": "a JPEG image", "image/gif": "a GIF image",
	"image/webp": "a WebP image", "application/pdf": "a PDF", textPlain: "plain text",
}

// Refusal is why Allowed refuses a file named name whose content starts
// with head, as a user reads it ("only PNG, JPG, GIF, WebP, PDF and text
// files", "the content is not a PNG image"), or "" when it allows the file.
func Refusal(name string, head []byte) string {
	if _, ok := Allowed(name, head); ok {
		return ""
	}
	want, ok := sniffed[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return "only PNG, JPG, GIF, WebP, PDF and text files"
	}
	return "the content is not " + kinds[want]
}

// Image reports whether contentType, as Allowed returns it, is an image the
// Desk can show inline.
func Image(contentType string) bool {
	switch mediaType(contentType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

func mediaType(contentType string) string {
	t, _, _ := strings.Cut(contentType, ";")
	return strings.ToLower(strings.TrimSpace(t))
}

// maxNameBytes caps the length of a cleaned file name.
const maxNameBytes = 100

// CleanName returns a file name that is safe on any file system and in a
// URL: the base name only (after the last / or \), every rune outside
// [A-Za-z0-9._-] replaced with _, leading dots stripped, and at most 100
// bytes, keeping a short extension. A name with nothing left is "file".
func CleanName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	s := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, name)
	s = strings.TrimLeft(s, ".")
	if len(s) > maxNameBytes {
		ext := filepath.Ext(s)
		if len(ext) > maxNameBytes/4 {
			ext = ""
		}
		s = s[:maxNameBytes-len(ext)] + ext
	}
	if s == "" {
		return "file"
	}
	return s
}

// LocalName is the file name Fetch writes a link to: the first 8 characters
// of its secret, a dash and its cleaned file name, for example
// "0123abcd-screenshot.png".
func LocalName(l Link) string {
	return CleanName(l.Secret[:min(8, len(l.Secret))] + "-" + CleanName(l.Filename))
}
