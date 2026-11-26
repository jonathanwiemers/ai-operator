package gitlabfake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

func TestUploadRoundTrip(t *testing.T) {
	f := New()
	ctx := context.Background()
	img, err := f.UploadFile(ctx, "group/app", "a.png", []byte("png"))
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := f.UploadFile(ctx, "group/app", "b.log", []byte("log!"))
	if !strings.HasPrefix(img.Markdown, "![a.png](/uploads/") || !strings.HasPrefix(doc.Markdown, "[b.log](/uploads/") {
		t.Fatalf("%q %q", img.Markdown, doc.Markdown)
	}
	parts := strings.Split(img.URL, "/")
	if len(parts) != 4 || len(parts[2]) != 32 {
		t.Fatalf("url %q", img.URL)
	}
	b, err := f.DownloadUpload(ctx, "group/app", parts[2], "a.png", 100)
	if err != nil || string(b) != "png" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := f.DownloadUpload(ctx, "group/app", parts[2], "a.png", 2); !errors.Is(err, gitlab.ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err := f.DownloadUpload(ctx, "group/app", parts[2], "zz.png", 100); gitlab.StatusCode(err) != 404 {
		t.Fatal(err)
	}
	if got := f.Uploads("group/app"); len(got) != 2 || got[0].Name != "a.png" || got[0].Size != 3 {
		t.Fatalf("%+v", got)
	}
}
