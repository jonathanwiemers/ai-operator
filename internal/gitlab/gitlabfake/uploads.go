package gitlabfake

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// UploadInfo describes one stored upload.
type UploadInfo struct {
	Name   string
	Secret string
	Size   int
}

var imageExts = []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp"}

// UploadFile stores the file under a random secret and answers like GitLab.
func (f *Fake) UploadFile(_ context.Context, project, name string, content []byte) (gitlab.Upload, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	secret := hex.EncodeToString(b)
	f.SeedUpload(project, secret, name, content)
	name = path.Base(name)
	rel := "/uploads/" + secret + "/" + name
	md := "[" + name + "](" + rel + ")"
	if slices.Contains(imageExts, strings.ToLower(path.Ext(name))) {
		md = "!" + md
	}
	return gitlab.Upload{
		Alt:      name,
		URL:      rel,
		FullPath: fmt.Sprintf("/-/project/%d/uploads/%s/%s", f.Project.ID, secret, name),
		Markdown: md,
	}, nil
}

// SeedUpload stores a file under the given secret.
func (f *Fake) SeedUpload(project, secret, name string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.uploads == nil {
		f.uploads = map[string]map[string][]byte{}
	}
	if f.uploads[project] == nil {
		f.uploads[project] = map[string][]byte{}
	}
	f.uploads[project][secret+"/"+path.Base(name)] = slices.Clone(content)
}

// DownloadUpload returns a stored file, a 404 for a missing one and
// gitlab.ErrTooLarge past max bytes.
func (f *Fake) DownloadUpload(_ context.Context, project, secret, filename string, max int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.uploads[project][secret+"/"+filename]
	if !ok {
		return nil, &gitlab.Error{StatusCode: 404, Method: "GET", Path: "/projects/:id/uploads/…", Body: "404 Not Found"}
	}
	if int64(len(data)) > max {
		return nil, gitlab.ErrTooLarge
	}
	return slices.Clone(data), nil
}

// Uploads lists the uploads of a project, by name.
func (f *Fake) Uploads(project string) []UploadInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []UploadInfo
	for k, data := range f.uploads[project] {
		secret, name, _ := strings.Cut(k, "/")
		out = append(out, UploadInfo{Name: name, Secret: secret, Size: len(data)})
	}
	slices.SortFunc(out, func(a, b UploadInfo) int { return strings.Compare(a.Name+a.Secret, b.Name+b.Secret) })
	return out
}

func (p *Projects) UploadFile(ctx context.Context, project, name string, content []byte) (gitlab.Upload, error) {
	f, err := p.route(project)
	if err != nil {
		return gitlab.Upload{}, err
	}
	return f.UploadFile(ctx, project, name, content)
}

func (p *Projects) DownloadUpload(ctx context.Context, project, secret, filename string, max int64) ([]byte, error) {
	f, err := p.route(project)
	if err != nil {
		return nil, err
	}
	return f.DownloadUpload(ctx, project, secret, filename, max)
}
