package attachments

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")
	gifBytes  = []byte("GIF89a\x01\x00\x01\x00")
	webpBytes = []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	pdfBytes  = []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	textBytes = []byte("2026-11-26 ERROR something broke\n")
)

func TestExtensionsMatchSniffTable(t *testing.T) {
	if len(Extensions) != len(sniffed) {
		t.Fatalf("Extensions has %d entries, the sniff table %d", len(Extensions), len(sniffed))
	}
	for _, ext := range Extensions {
		if _, ok := sniffed[ext]; !ok || ext != strings.ToLower(ext) {
			t.Errorf("extension %q missing from the sniff table or not lower case", ext)
		}
	}
	want := []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf", ".txt", ".log", ".md", ".json", ".yaml", ".yml", ".csv"}
	if !slices.Equal(Extensions, want) {
		t.Errorf("Extensions = %v", Extensions)
	}
}

func TestAllowed(t *testing.T) {
	const utf8 = "text/plain; charset=utf-8"
	for _, c := range []struct {
		name string
		head []byte
		want string // "" means refused
	}{
		{"a.png", pngBytes, "image/png"},
		{"a.jpg", jpegBytes, "image/jpeg"},
		{"a.jpeg", jpegBytes, "image/jpeg"},
		{"a.gif", gifBytes, "image/gif"},
		{"a.webp", webpBytes, "image/webp"},
		{"a.pdf", pdfBytes, "application/pdf"},
		{"a.txt", textBytes, utf8},
		{"a.log", textBytes, utf8},
		{"a.md", []byte("# Title\n\n- item\n"), utf8},
		{"a.json", []byte(`{"a": [1, 2]}`), utf8},
		{"a.yaml", []byte("a: 1\nb: [x]\n"), utf8},
		{"a.yml", []byte("a: 1\n"), utf8},
		{"a.csv", []byte("a,b\n1,2\n"), utf8},
		{"utf16.txt", []byte("\xfe\xff\x00h\x00i"), utf8},
		{"empty.txt", nil, utf8},
		{"SHOT.PNG", pngBytes, "image/png"},
		{"Photo.JpEg", jpegBytes, "image/jpeg"},
		{"NOTES.TXT", textBytes, utf8},
		{"dir/a.png", pngBytes, "image/png"},
		// refused
		{"text.png", textBytes, ""},
		{"png.txt", pngBytes, ""},
		{"jpeg.png", jpegBytes, ""},
		{"pdf.jpg", pdfBytes, ""},
		{"page.md", []byte("<!DOCTYPE html><html></html>"), ""},
		{"binary.log", []byte("\x00\x01\x02\x03"), ""},
		{"a.zip", []byte("PK\x03\x04"), ""},
		{"a.exe", []byte("MZ\x90\x00"), ""},
		{"a.svg", []byte("<svg></svg>"), ""},
		{"README", textBytes, ""},
		{"png", pngBytes, ""},
		{"", pngBytes, ""},
	} {
		got, ok := Allowed(c.name, c.head)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Allowed(%q) = %q, %v; want %q", c.name, got, ok, c.want)
		}
	}
}

func TestImage(t *testing.T) {
	for ct, want := range map[string]bool{
		"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
		"Image/PNG; x=y": true, "application/pdf": false, "text/plain; charset=utf-8": false,
		"image/svg+xml": false, "": false,
	} {
		if got := Image(ct); got != want {
			t.Errorf("Image(%q) = %v", ct, got)
		}
	}
}

func TestCleanName(t *testing.T) {
	long := strings.Repeat("a", 150)
	for in, want := range map[string]string{
		"screenshot.png":       "screenshot.png",
		"Bild 1 (2).PNG":       "Bild_1__2_.PNG",
		"résumé.pdf":           "r_sum_.pdf",
		"../x.png":             "x.png",
		`..\..\x.png`:          "x.png",
		"/etc/passwd":          "passwd",
		".hidden.txt":          "hidden.txt",
		"...":                  "file",
		"":                     "file",
		"a/":                   "file",
		"a\x00b.txt":           "a_b.txt",
		"%2e%2e.txt":           "_2e_2e.txt",
		long + ".png":          strings.Repeat("a", 96) + ".png",
		long:                   strings.Repeat("a", 100),
		"a." + long:            "a." + strings.Repeat("a", 98),
		"emoji-😀.json":         "emoji-_.json",
		"tab\tand\nnewline.md": "tab_and_newline.md",
	} {
		got := CleanName(in)
		if got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
		if len(got) > maxNameBytes {
			t.Errorf("CleanName(%q) is %d bytes", in, len(got))
		}
	}
}

func TestLocalName(t *testing.T) {
	const secret = "0123abcdef0123456789abcdef012345"
	dir := t.TempDir()
	for file, want := range map[string]string{
		"screenshot.png":                 "0123abcd-screenshot.png",
		"../x.png":                       "0123abcd-x.png",
		"..":                             "0123abcd-file",
		"a b.log":                        "0123abcd-a_b.log",
		strings.Repeat("z", 200) + ".md": "0123abcd-" + strings.Repeat("z", 88) + ".md",
	} {
		got := LocalName(Link{Secret: secret, Filename: file})
		if got != want {
			t.Errorf("LocalName(%q) = %q, want %q", file, got, want)
		}
	}
	for _, l := range []Link{
		{Secret: "../../etc", Filename: "x.png"},
		{Secret: "", Filename: "/etc/passwd"},
		{Secret: "ab/", Filename: `..\..`},
		{Secret: "..", Filename: ".."},
	} {
		name := LocalName(l)
		if !filepath.IsLocal(name) || filepath.Dir(filepath.Join(dir, name)) != dir {
			t.Errorf("LocalName(%+v) = %q escapes the directory", l, name)
		}
	}
}
