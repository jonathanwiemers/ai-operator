package gitlab

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUploadFileSendsMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/v4/projects/group%2Fapp/uploads" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.EscapedPath())
		}
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
			t.Errorf("content type %q", r.Header.Get("Content-Type"))
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		p, err := mr.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(p)
		if p.FormName() != "file" || p.FileName() != "shot.png" || string(body) != "bytes" || p.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("part %q %q %q %q", p.FormName(), p.FileName(), body, p.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"alt":"shot","url":"/uploads/s/shot.png","full_path":"/f","markdown":"![shot](/uploads/s/shot.png)"}`))
	}))
	defer srv.Close()
	up, err := New(srv.URL, "tok").UploadFile(context.Background(), "group/app", "shot.png", []byte("bytes"))
	if err != nil || up.URL != "/uploads/s/shot.png" || up.Markdown != "![shot](/uploads/s/shot.png)" || up.FullPath != "/f" || up.Alt != "shot" {
		t.Fatalf("%+v %v", up, err)
	}
}

func TestUploadServerErrorIsNotRetriedButDownloadIs(t *testing.T) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	if _, err := c.UploadFile(context.Background(), "g/a", "a.txt", []byte("x")); StatusCode(err) != 500 || n.Load() != 1 {
		t.Fatalf("upload: %v after %d attempts", err, n.Load())
	}
	n.Store(0)
	if _, err := c.DownloadUpload(context.Background(), "g/a", "secretsecret", "a.txt", 10); StatusCode(err) != 500 || n.Load() != int64(maxAttempts) {
		t.Fatalf("download: %v after %d attempts", err, n.Load())
	}
}

func TestDownloadUploadCapAnd404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/g%2Fa/uploads/abc/my%20file.bin" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()
	if b, err := c.DownloadUpload(ctx, "g/a", "abc", "my file.bin", 10); err != nil || string(b) != "0123456789" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := c.DownloadUpload(ctx, "g/a", "abc", "my file.bin", 9); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
	_, err := c.DownloadUpload(ctx, "g/a", "topsecret", "x.bin", 10)
	if StatusCode(err) != 404 || strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("want 404 without the secret: %v", err)
	}
}
