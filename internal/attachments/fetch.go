package attachments

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// Downloader reads a GitLab upload, as gitlab.API's DownloadUpload does.
// project is the path with namespace or the numeric ID, filename is
// Link.Filename (decoded; the Downloader escapes it). It fails past max
// bytes. Fetch reports an error that matches fs.ErrNotExist as not found and
// a *http.MaxBytesError as too large.
type Downloader interface {
	DownloadUpload(ctx context.Context, project, secret, filename string, max int64) ([]byte, error)
}

// Caps limits what one Fetch takes. Zero (or negative) fields fall back to
// MaxTurnFiles, MaxTurnBytes and MaxFileBytes.
type Caps struct {
	Files     int
	Bytes     int64
	FileBytes int64
}

// Result is what Fetch did with one link: exactly one of Path (the file
// written) and Reason (a short phrase saying why not, without the secret or
// the content) is set.
type Result struct {
	Link   Link
	Path   string
	Reason string
}

// Fetch downloads links of project into dir, in the given order (callers put
// the newest first), and returns one Result per link. Each file is read with
// caps.FileBytes as the limit, must pass Allowed, and is written to
// dir/LocalName(l). Once caps.Files files or caps.Bytes bytes are written,
// the remaining links are refused; a smaller file later may still fit the
// byte cap. A link given twice gets the result of its first time. One failure
// never stops the rest.
func Fetch(ctx context.Context, dl Downloader, project string, links []Link, dir string, caps Caps) []Result {
	if caps.Files <= 0 {
		caps.Files = MaxTurnFiles
	}
	if caps.Bytes <= 0 {
		caps.Bytes = MaxTurnBytes
	}
	if caps.FileBytes <= 0 {
		caps.FileBytes = MaxFileBytes
	}
	f := fetcher{ctx: ctx, dl: dl, project: project, dir: dir, caps: caps}
	out := make([]Result, len(links))
	first := map[string]int{} // local name -> index of its first link
	for i, l := range links {
		name := LocalName(l)
		if j, ok := first[name]; ok {
			out[i] = out[j]
			if links[j].Secret != l.Secret || links[j].Filename != l.Filename {
				out[i] = Result{Reason: "same local name as another attachment"}
			}
			out[i].Link = l
			continue
		}
		first[name] = i
		out[i] = Result{Link: l}
		if path, err := f.fetch(l, name); err != nil {
			out[i].Reason = err.Error()
		} else {
			out[i].Path = path
		}
	}
	return out
}

type fetcher struct {
	ctx     context.Context
	dl      Downloader
	project string
	dir     string
	caps    Caps
	files   int
	bytes   int64
	mkdir   bool
}

// fetch downloads and writes one link, or returns the reason it did not.
func (f *fetcher) fetch(l Link, name string) (string, error) {
	switch {
	case !isSecret(l.Secret) || !segment(l.Filename) || !filepath.IsLocal(name):
		return "", errors.New("not a GitLab upload link")
	case !allowedExt(l.Filename):
		return "", errors.New("not an allowed file type")
	case f.files >= f.caps.Files:
		return "", fmt.Errorf("over the %d-file limit for a turn", f.caps.Files)
	}
	content, err := f.dl.DownloadUpload(f.ctx, f.project, l.Secret, l.Filename, f.caps.FileBytes)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge) || err == nil && int64(len(content)) > f.caps.FileBytes:
		return "", fmt.Errorf("larger than %s", size(f.caps.FileBytes))
	case errors.Is(err, fs.ErrNotExist):
		return "", errors.New("not found in GitLab")
	case err != nil:
		return "", errors.New("download failed")
	}
	if _, ok := Allowed(l.Filename, content); !ok {
		return "", errors.New("not an allowed file type")
	}
	if f.bytes+int64(len(content)) > f.caps.Bytes {
		return "", fmt.Errorf("over the %s limit for a turn", size(f.caps.Bytes))
	}
	if !f.mkdir {
		if err := os.MkdirAll(f.dir, 0o755); err != nil { // #nosec G301 -- attachments for the agent in the runner's exchange dir; the pod runs every container as uid/gid 1000
			return "", errors.New("could not be saved")
		}
		f.mkdir = true
	}
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil { // #nosec G306 -- a file the agent reads, linked in text it already has; the pod runs every container as uid/gid 1000
		return "", errors.New("could not be saved")
	}
	f.files++
	f.bytes += int64(len(content))
	return path, nil
}

// size formats n bytes for a reason: "10 MiB", "4 KiB" or "100 bytes".
func size(n int64) string {
	switch {
	case n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}
