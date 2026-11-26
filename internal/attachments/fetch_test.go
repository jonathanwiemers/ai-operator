package attachments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDL serves files by secret+"/"+filename and records each call.
type fakeDL struct {
	files map[string][]byte
	errs  map[string]error
	calls []string
	maxes []int64
}

func (f *fakeDL) DownloadUpload(_ context.Context, project, secret, filename string, max int64) ([]byte, error) {
	key := secret + "/" + filename
	f.calls = append(f.calls, project+":"+key)
	f.maxes = append(f.maxes, max)
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	b, ok := f.files[key]
	if !ok {
		return nil, fmt.Errorf("gitlab: GET uploads: 404: %w", fs.ErrNotExist)
	}
	return b, nil
}

// secretN returns a distinct valid secret for n.
func secretN(n int) string { return fmt.Sprintf("%08x%024x", n, n) }

func link(n int, name string) Link { return Link{Secret: secretN(n), Filename: name} }

func reasons(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Reason
		if (r.Path == "") == (r.Reason == "") {
			out[i] = "BAD: path " + r.Path + " reason " + r.Reason
		}
	}
	return out
}

func check(t *testing.T, rs []Result, links []Link, want []string) {
	t.Helper()
	if len(rs) != len(links) {
		t.Fatalf("%d results for %d links", len(rs), len(links))
	}
	for i, r := range rs {
		if r.Link != links[i] {
			t.Errorf("result %d is for %+v, want %+v", i, r.Link, links[i])
		}
		if strings.Contains(r.Reason, r.Link.Secret) && r.Link.Secret != "" {
			t.Errorf("reason %q contains the secret", r.Reason)
		}
	}
	if got := reasons(rs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("reasons = %q\nwant      %q", got, want)
	}
}

func TestFetchWritesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plan", "attachments")
	links := []Link{link(1, "shot.png"), link(2, "app.log"), link(3, "My Doc.pdf")}
	dl := &fakeDL{files: map[string][]byte{
		secretN(1) + "/shot.png":   pngBytes,
		secretN(2) + "/app.log":    textBytes,
		secretN(3) + "/My Doc.pdf": pdfBytes,
	}}
	rs := Fetch(context.Background(), dl, "group/app", links, dir, Caps{})
	check(t, rs, links, []string{"", "", ""})
	for i, content := range [][]byte{pngBytes, textBytes, pdfBytes} {
		want := filepath.Join(dir, LocalName(links[i]))
		if rs[i].Path != want {
			t.Errorf("path %q, want %q", rs[i].Path, want)
		}
		got, err := os.ReadFile(rs[i].Path)
		if err != nil || !bytes.Equal(got, content) {
			t.Errorf("file %s: %q, %v", rs[i].Path, got, err)
		}
		if st, _ := os.Stat(rs[i].Path); st.Mode().Perm() != 0o644 {
			t.Errorf("file mode %v", st.Mode())
		}
	}
	if filepath.Base(rs[2].Path) != "00000003-My_Doc.pdf" {
		t.Errorf("local name %q", filepath.Base(rs[2].Path))
	}
	for i, c := range dl.calls {
		if !strings.HasPrefix(c, "group/app:") || dl.maxes[i] != MaxFileBytes {
			t.Errorf("call %q with max %d", c, dl.maxes[i])
		}
	}
}

func TestFetchFileCap(t *testing.T) {
	dl := &fakeDL{files: map[string][]byte{}}
	var links []Link
	for i := range 5 {
		links = append(links, link(i, "f.txt"))
		dl.files[secretN(i)+"/f.txt"] = textBytes
	}
	links = append(links[:1], append([]Link{link(9, "gone.png")}, links[1:]...)...)
	rs := Fetch(context.Background(), dl, "p", links, t.TempDir(), Caps{Files: 3})
	check(t, rs, links, []string{"", "not found in GitLab", "", "",
		"over the 3-file limit for a turn", "over the 3-file limit for a turn"})
	if len(dl.calls) != 4 {
		t.Errorf("%d downloads, want 4: links over the file cap are not downloaded", len(dl.calls))
	}
}

func TestFetchByteCaps(t *testing.T) {
	big := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, 100)...)
	small := pngBytes
	dl := &fakeDL{files: map[string][]byte{
		secretN(1) + "/a.png": big,   // 116 bytes
		secretN(2) + "/b.png": big,   // would make 232
		secretN(3) + "/c.png": small, // still fits: 132
		secretN(4) + "/d.png": append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, 200)...),
		secretN(5) + "/e.png": append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, 16)...), // 32 bytes: 164 > 150
	}}
	links := []Link{link(1, "a.png"), link(2, "b.png"), link(3, "c.png"), link(4, "d.png"), link(5, "e.png")}
	rs := Fetch(context.Background(), dl, "p", links, t.TempDir(), Caps{Bytes: 150, FileBytes: 200})
	check(t, rs, links, []string{"", "over the 150 bytes limit for a turn", "", "larger than 200 bytes",
		"over the 150 bytes limit for a turn"})
	for _, m := range dl.maxes {
		if m != 200 {
			t.Errorf("max %d passed down, want 200", m)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(rs[0].Path), LocalName(links[1]))); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file over the byte cap written: %v", err)
	}
}

func TestFetchFailuresDoNotStopTheRest(t *testing.T) {
	dir := t.TempDir()
	links := []Link{
		link(1, "a.png"),
		link(2, "broken.png"),
		link(3, "missing.png"),
		link(4, "huge.png"),
		link(5, "fake.png"),
		link(6, "archive.zip"),
		{Secret: "../../etc", Filename: "x.png"},
		{Secret: secretN(7), Filename: "../x.png"},
		link(8, "b.txt"),
	}
	dl := &fakeDL{
		files: map[string][]byte{
			secretN(1) + "/a.png":    pngBytes,
			secretN(5) + "/fake.png": textBytes,
			secretN(8) + "/b.txt":    textBytes,
		},
		errs: map[string]error{
			secretN(2) + "/broken.png": errors.New("gitlab: GET uploads: 500 " + secretN(2)),
			secretN(4) + "/huge.png":   fmt.Errorf("reading: %w", &http.MaxBytesError{Limit: MaxFileBytes}),
		},
	}
	rs := Fetch(context.Background(), dl, "p", links, dir, Caps{})
	check(t, rs, links, []string{"", "download failed", "not found in GitLab", "larger than 10 MiB",
		"not an allowed file type", "not an allowed file type", "not a GitLab upload link",
		"not a GitLab upload link", ""})
	if len(dl.calls) != 6 {
		t.Errorf("downloads %q: refused names and links are not downloaded", dl.calls)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("%d files written, want 2", len(entries))
	}
}

// lyingDL ignores max.
type lyingDL struct{ content []byte }

func (l lyingDL) DownloadUpload(context.Context, string, string, string, int64) ([]byte, error) {
	return l.content, nil
}

func TestFetchContentOverMax(t *testing.T) {
	links := []Link{link(1, "a.txt")}
	rs := Fetch(context.Background(), lyingDL{bytes.Repeat([]byte("a"), 2048)}, "p", links, t.TempDir(), Caps{FileBytes: 1024})
	check(t, rs, links, []string{"larger than 1 KiB"})
}

func TestFetchDuplicates(t *testing.T) {
	dl := &fakeDL{files: map[string][]byte{
		secretN(1) + "/a.png": pngBytes,
		secretN(2) + "/a.png": pngBytes,
	}}
	// Same first 8 characters of the secret, same name: another upload.
	clash := Link{Secret: secretN(1)[:8] + strings.Repeat("f", 24), Filename: "a.png"}
	links := []Link{link(1, "a.png"), link(2, "gone.png"), {Raw: "/x", Secret: secretN(1), Filename: "a.png"}, link(2, "gone.png"), clash}
	rs := Fetch(context.Background(), dl, "p", links, t.TempDir(), Caps{})
	check(t, rs, links, []string{"", "not found in GitLab", "", "not found in GitLab", "same local name as another attachment"})
	if rs[2].Path != rs[0].Path || len(dl.calls) != 2 {
		t.Errorf("a repeated link is downloaded again: %q, paths %q %q", dl.calls, rs[0].Path, rs[2].Path)
	}
}

func TestFetchUnwritableDir(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dl := &fakeDL{files: map[string][]byte{secretN(1) + "/a.png": pngBytes}}
	links := []Link{link(1, "a.png")}
	check(t, Fetch(context.Background(), dl, "p", links, filepath.Join(file, "sub"), Caps{}), links, []string{"could not be saved"})

	// The directory exists but the file cannot be written.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, LocalName(links[0])), 0o755); err != nil {
		t.Fatal(err)
	}
	check(t, Fetch(context.Background(), dl, "p", links, dir, Caps{}), links, []string{"could not be saved"})
}

func TestFetchNoLinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "attachments")
	if rs := Fetch(context.Background(), &fakeDL{}, "p", nil, dir, Caps{}); len(rs) != 0 {
		t.Errorf("results %+v", rs)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dir created without a file: %v", err)
	}
}

func TestDefaultReasons(t *testing.T) {
	for n, want := range map[int64]string{MaxFileBytes: "10 MiB", MaxTurnBytes: "50 MiB", 4096: "4 KiB", 1000: "1000 bytes"} {
		if got := size(n); got != want {
			t.Errorf("size(%d) = %q", n, got)
		}
	}
	dl := &fakeDL{files: map[string][]byte{}}
	var links []Link
	for i := range MaxTurnFiles + 1 {
		links = append(links, link(i, "f.txt"))
		dl.files[secretN(i)+"/f.txt"] = textBytes
	}
	rs := Fetch(context.Background(), dl, "p", links, t.TempDir(), Caps{Files: -1})
	if got := rs[MaxTurnFiles].Reason; got != "over the 20-file limit for a turn" {
		t.Errorf("reason %q", got)
	}
}
