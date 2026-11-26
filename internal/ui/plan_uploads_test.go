package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/attachments"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab/gitlabfake"
)

const shopUploads = shopPlans + "/uploads"

var (
	uploadPNG  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01")
	uploadPDF  = []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n")
	uploadText = []byte("2026-11-26 ERROR the header overlaps the tabs\n")
)

// uploadEnv is a chat-enabled UI with shop's plans on an in-memory GitLab,
// ada@example.com logged in with role (leads steer team-a, viewers view
// it), connected to GitLab when connect, with the server's log captured.
func uploadEnv(t *testing.T, role string, connect bool) (*testEnv, *gitlabfake.Fake, func() string) {
	t.Helper()
	env, _ := steeringEnvOf(t, withChat(oidcConfig(
		Rule{Roles: []string{"leads"}, Projects: []string{"team-a/*"}, Steer: true},
		Rule{Roles: []string{"viewers"}, Projects: []string{"team-a/*"}},
	)))
	addPlans(t, env)
	env.server.SetResearchWriter(env.reader)
	env.idp.user = map[string]any{"sub": "u1", "email": "ada@example.com"}
	env.idp.info = map[string]any{"urn:zitadel:iam:org:project:roles": map[string]any{role: map[string]any{}}}
	env.login(t, "/")
	if connect {
		env.connectGitLab(t, "good-code")
	}
	c := captureLog(env)
	return env, followUpGitLab(env), func() string {
		c.mu.Lock()
		defer c.mu.Unlock()
		return strings.Join(c.lines, "\n")
	}
}

// filePart is one part of an upload request.
type filePart struct {
	field, name string
	content     []byte
}

// upload posts the parts as multipart/form-data and returns the status and
// the decoded JSON answer.
func (e *testEnv) upload(t *testing.T, path string, parts ...filePart) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		var w io.Writer
		var err error
		if p.name == "" {
			w, err = mw.CreateFormField(p.field)
		} else {
			w, err = mw.CreateFormFile(p.field, p.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(p.content)
	}
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestPlanUploadAllowedTypes(t *testing.T) {
	env, fake, logs := uploadEnv(t, "leads", true)
	for _, c := range []struct {
		name    string
		content []byte
		clean   string
		ct      string
		image   bool
	}{
		{"screen shot.png", uploadPNG, "screen_shot.png", "image/png", true},
		{"spec.pdf", uploadPDF, "spec.pdf", "application/pdf", false},
		{"build.log", uploadText, "build.log", "text/plain; charset=utf-8", false},
	} {
		code, out := env.upload(t, shopUploads, filePart{"file", c.name, c.content})
		if code != http.StatusCreated {
			t.Fatalf("%s: %d %v", c.name, code, out)
		}
		url, _ := out["url"].(string)
		md, _ := out["markdown"].(string)
		if out["name"] != c.clean || out["size"] != float64(len(c.content)) || out["contentType"] != c.ct ||
			!strings.HasPrefix(url, "/uploads/") || !strings.HasSuffix(url, "/"+c.clean) || strings.HasPrefix(md, "!") != c.image {
			t.Errorf("%s: %v", c.name, out)
		}
	}
	ups := fake.Uploads("acme/shop")
	if len(ups) != 3 || ups[0].Name != "build.log" || ups[1].Name != "screen_shot.png" || ups[1].Size != len(uploadPNG) {
		t.Fatalf("uploads: %+v", ups)
	}
	l := logs()
	if !strings.Contains(l, `"action"="plan.upload"`) || !strings.Contains(l, `"size"=`+strconv.Itoa(len(uploadPNG))) || !strings.Contains(l, `"type"="image/png"`) {
		t.Errorf("audit line: %s", l)
	}
	for _, u := range ups {
		if strings.Contains(l, u.Secret) || strings.Contains(l, "/uploads/") || strings.Contains(l, "overlaps") {
			t.Errorf("the log has a secret, link or content: %s", l)
		}
	}
}

func TestPlanUploadRefused(t *testing.T) {
	env, fake, _ := uploadEnv(t, "leads", true)
	for _, c := range []struct {
		what  string
		parts []filePart
		code  int
		msg   string
	}{
		{"zip", []filePart{{"file", "a.zip", []byte("PK\x03\x04")}}, 400, "a.zip: only PNG, JPG, GIF, WebP, PDF and text files"},
		{"spoofed png", []filePart{{"file", "shot.png", uploadText}}, 400, "shot.png: the content is not a PNG image"},
		{"binary log", []filePart{{"file", "a.log", []byte("\x00\x01\x02")}}, 400, "a.log: the content is not plain text"},
		{"empty", []filePart{{"file", "a.txt", nil}}, 400, "a.txt: the file is empty"},
		{"no part", nil, 400, "send exactly one file"},
		{"other field", []filePart{{"doc", "a.png", uploadPNG}}, 400, "send exactly one file"},
		{"not a file", []filePart{{"file", "", uploadText}}, 400, "send exactly one file"},
		{"two files", []filePart{{"file", "a.png", uploadPNG}, {"file", "b.png", uploadPNG}}, 400, "send exactly one file"},
		{"just over", []filePart{{"file", "big.txt", bytes.Repeat([]byte("a"), attachments.MaxFileBytes+1)}}, 413, "big.txt: larger than 10 MiB"},
		{"11 MiB", []filePart{{"file", "huge.txt", bytes.Repeat([]byte("a"), 11<<20)}}, 413, "huge.txt: larger than 10 MiB"},
	} {
		code, out := env.upload(t, shopUploads, c.parts...)
		if code != c.code || out["field"] != "file" || !strings.HasPrefix(out["error"].(string), c.msg) {
			t.Errorf("%s: %d %v", c.what, code, out)
		}
	}
	// Not multipart at all.
	if code, out := env.postJSON(t, shopUploads, map[string]string{"file": "x"}); code != 400 || out["field"] != "file" {
		t.Errorf("JSON: %d %v", code, out)
	}
	if ups := fake.Uploads("acme/shop"); len(ups) != 0 {
		t.Errorf("uploads: %+v", ups)
	}
}

func TestPlanUploadNeedsSteerAndGitLab(t *testing.T) {
	viewer, fake, _ := uploadEnv(t, "viewers", true)
	if code, out := viewer.upload(t, shopUploads, filePart{"file", "a.png", uploadPNG}); code != 403 || out["error"] != "only the project's owners change its plans" {
		t.Errorf("viewer: %d %v", code, out)
	}
	if len(fake.Uploads("acme/shop")) != 0 {
		t.Error("the viewer uploaded")
	}
	owner, fake, _ := uploadEnv(t, "leads", false)
	if code, out := owner.upload(t, shopUploads, filePart{"file", "a.png", uploadPNG}); code != 409 || out["error"] != "connect your GitLab account first" {
		t.Errorf("not connected: %d %v", code, out)
	}
	if len(fake.Uploads("acme/shop")) != 0 {
		t.Error("uploaded without a connected GitLab")
	}
}

// failingUploads is a GitLab whose uploads fail with a body that must not
// reach the client or the log.
type failingUploads struct{ *gitlabfake.Fake }

func (failingUploads) UploadFile(context.Context, string, string, []byte) (gitlab.Upload, error) {
	return gitlab.Upload{}, &gitlab.Error{StatusCode: 500, Method: "POST", Path: "/projects/acme%2Fshop/uploads", Body: "stored at /uploads/0123"}
}

func TestPlanUploadGitLabFails(t *testing.T) {
	env, fake, logs := uploadEnv(t, "leads", true)
	env.server.gitlab.newClient = func(string, string) gitlab.API { return failingUploads{fake} }
	code, out := env.upload(t, shopUploads, filePart{"file", "a.png", uploadPNG})
	if code != http.StatusBadGateway || out["error"] != "uploading a.png to GitLab failed, try again" {
		t.Errorf("%d %v", code, out)
	}
	if l := logs(); !strings.Contains(l, `"status"=500`) || strings.Contains(l, "stored at") || strings.Contains(l, `"action"="plan.upload"`) {
		t.Errorf("log: %s", l)
	}
}

// On the stand-in project of a dormant set entry, the file goes to the
// entry's GitLab project.
func TestPlanUploadStandIn(t *testing.T) {
	gl := newFakeGitLab(t)
	cfg := setPlansConfig()
	cfg.GitLab = GitLabConfig{URL: gl.srv.URL, ClientID: "gl-client"}
	cfg.GitLabClientSecret = "gl-secret"
	env := setPlansEnvOf(t, cfg, "leads")
	var set aiv1.AgentProjectSet
	if err := env.reader.Get(context.Background(), client.ObjectKey{Namespace: "team-a", Name: "acme"}, &set); err != nil {
		t.Fatal(err)
	}
	set.Spec.GitLab.URL = gl.srv.URL
	if err := env.reader.Update(context.Background(), &set); err != nil {
		t.Fatal(err)
	}
	env.server.SetResearchWriter(env.reader)
	env.connectGitLab(t, "good-code")
	fake := followUpGitLab(env)
	code, out := env.upload(t, fooPlans+"/uploads", filePart{"file", "shot.png", uploadPNG})
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	ups := fake.Uploads("acme/tools/foo")
	if len(ups) != 1 || ups[0].Name != "shot.png" {
		t.Fatalf("uploads: %+v", ups)
	}
	resp := env.getFile(t, fooPlans+"/uploads/"+ups[0].Secret+"/shot.png")
	if resp.code != 200 || !bytes.Equal(resp.body, uploadPNG) {
		t.Errorf("read back: %d", resp.code)
	}
}

type fileResponse struct {
	code   int
	header http.Header
	body   []byte
}

func (e *testEnv) getFile(t *testing.T, path string) fileResponse {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return fileResponse{resp.StatusCode, resp.Header, body}
}

const uploadSecretHex = "0123456789abcdef0123456789ABCDEF"

func TestPlanUploadRead(t *testing.T) {
	// A viewer without steer reads the files.
	env, fake, logs := uploadEnv(t, "viewers", true)
	for _, c := range []struct {
		name            string
		content         []byte
		ct, disposition string
	}{
		{"shot.png", uploadPNG, "image/png", `inline; filename="shot.png"; filename*=UTF-8''shot.png`},
		{"spec.pdf", uploadPDF, "application/pdf", `inline; filename="spec.pdf"; filename*=UTF-8''spec.pdf`},
		{"build.log", uploadText, "text/plain; charset=utf-8", `attachment; filename="build.log"; filename*=UTF-8''build.log`},
	} {
		fake.SeedUpload("acme/shop", uploadSecretHex, c.name, c.content)
		r := env.getFile(t, shopUploads+"/"+uploadSecretHex+"/"+c.name)
		h := r.header
		if r.code != 200 || !bytes.Equal(r.body, c.content) {
			t.Fatalf("%s: %d %q", c.name, r.code, r.body)
		}
		if h.Get("Content-Type") != c.ct || h.Get("Content-Disposition") != c.disposition || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != "sandbox" || h.Get("Cache-Control") != "private, max-age=3600" {
			t.Errorf("%s: headers %v", c.name, h)
		}
	}
	if l := logs(); strings.Contains(strings.ToLower(l), strings.ToLower(uploadSecretHex)) {
		t.Errorf("the log has the secret: %s", l)
	}
}

func TestPlanUploadReadRefused(t *testing.T) {
	env, fake, _ := uploadEnv(t, "viewers", true)
	fake.SeedUpload("acme/shop", uploadSecretHex, "fake.png", uploadText)
	fake.SeedUpload("acme/shop", uploadSecretHex, "a.zip", []byte("PK\x03\x04"))
	fake.SeedUpload("acme/shop", uploadSecretHex, "big.txt", bytes.Repeat([]byte("a"), attachments.MaxFileBytes+1))
	for path, want := range map[string]int{
		uploadSecretHex + "/fake.png":               415,
		uploadSecretHex + "/a.zip":                  415,
		uploadSecretHex + "/big.txt":                413,
		uploadSecretHex + "/missing.png":            404, // GitLab's 404
		"0123/fake.png":                             404, // not a secret
		"0123456789abcdef0123456789abcdeg/fake.png": 404,
		uploadSecretHex + "/a%20b.png":              404, // not a clean name
		uploadSecretHex + "/..":                     404,
	} {
		if r := env.getFile(t, shopUploads+"/"+path); r.code != want {
			t.Errorf("%s: %d, want %d", path, r.code, want)
		}
	}
	// Another project's file is not this project's.
	fake.SeedUpload("acme/web", uploadSecretHex, "other.png", uploadPNG)
	if r := env.getFile(t, shopUploads+"/"+uploadSecretHex+"/other.png"); r.code != 404 {
		t.Errorf("other project: %d", r.code)
	}
	unconnected, fake2, _ := uploadEnv(t, "viewers", false)
	fake2.SeedUpload("acme/shop", uploadSecretHex, "shot.png", uploadPNG)
	if r := unconnected.getFile(t, shopUploads+"/"+uploadSecretHex+"/shot.png"); r.code != 409 {
		t.Errorf("not connected: %d %s", r.code, r.body)
	}
}
