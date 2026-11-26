package ui

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/jonathanwiemers/ai-operator/internal/attachments"
	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// PlanUploadView is a file uploaded to the project's GitLab uploads for a
// plan message: the cleaned name, its size in bytes and served type, and
// GitLab's link and Markdown, which the message carries.
type PlanUploadView struct {
	Name        string `json:"name"`
	Size        int    `json:"size"`
	ContentType string `json:"contentType"`
	URL         string `json:"url"`
	Markdown    string `json:"markdown"`
}

// uploadHeadroom is what an upload request may carry beyond the file: the
// multipart boundaries and part headers.
const uploadHeadroom = 64 << 10

// uploadSecret is the secret segment of a GitLab upload link.
var uploadSecret = regexp.MustCompile(`^(?i)[0-9a-f]{32}$`)

// uploadTooLarge is the 413 of an upload or a read past the file limit.
const uploadTooLarge = "larger than 10 MiB"

// uploadPlanFile stores the owner's file, one multipart part named "file",
// in the GitLab uploads of the project (or of a stand-in's set entry) with
// the owner's GitLab token. The file is read into memory only, never to
// disk, and checked by extension and content first. The audit line names
// the size and type, never the content, link or secret.
func (s *Server) uploadPlanFile(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.ownPlans(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachments.MaxFileBytes+uploadHeadroom)
	name, content, ok := readUploadPart(w, r)
	if !ok {
		return
	}
	ct, ok := attachments.Allowed(name, content)
	if !ok {
		writeErrorResponse(w, http.StatusBadRequest, ErrorResponse{Error: name + ": " + attachments.Refusal(name, content), Field: "file"})
		return
	}
	ap := sc.Project
	gl, tok, ok := s.writingGitLab(w, r, ap)
	if !ok {
		return
	}
	clean := attachments.CleanName(name)
	up, err := gl.UploadFile(r.Context(), ap.Spec.GitLab.Project, clean, content)
	if err != nil {
		// GitLab's answer may echo what it stored: only its status is logged.
		var ge *gitlab.Error
		status := 0
		if errors.As(err, &ge) {
			status = ge.StatusCode
		}
		s.log.Info("plan upload failed", "user", principalOf(r).Username, "namespace", ap.Namespace, "project", ap.Name, "status", status)
		writeError(w, http.StatusBadGateway, "uploading "+clean+" to GitLab failed, try again")
		return
	}
	s.log.Info("audit", "action", "plan.upload", "user", principalOf(r).Username, "gitlabUser", tok.Username,
		"namespace", ap.Namespace, "project", ap.Name, "size", len(content), "type", ct)
	writeJSON(w, http.StatusCreated, PlanUploadView{Name: clean, Size: len(content), ContentType: ct, URL: up.URL, Markdown: up.Markdown})
}

// readUploadPart reads the request's one multipart part "file" with its
// file name into memory, at most attachments.MaxFileBytes. It answers 400
// (field file) or 413 itself.
func readUploadPart(w http.ResponseWriter, r *http.Request) (string, []byte, bool) {
	fail := func(err error, name, msg string) (string, []byte, bool) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			msg = "the file is " + uploadTooLarge
			if name != "" {
				msg = name + ": " + uploadTooLarge
			}
			writeErrorResponse(w, http.StatusRequestEntityTooLarge, ErrorResponse{Error: msg, Field: "file"})
		} else {
			writeErrorResponse(w, http.StatusBadRequest, ErrorResponse{Error: msg, Field: "file"})
		}
		return "", nil, false
	}
	const onePart = "send exactly one file, as the form field file"
	mr, err := r.MultipartReader()
	if err != nil {
		return fail(err, "", onePart)
	}
	part, err := mr.NextPart()
	if err != nil {
		return fail(err, "", onePart)
	}
	name := part.FileName()
	if part.FormName() != "file" || name == "" {
		return fail(nil, "", onePart)
	}
	content, err := io.ReadAll(io.LimitReader(part, attachments.MaxFileBytes+1))
	if err == nil && len(content) > attachments.MaxFileBytes {
		err = &http.MaxBytesError{Limit: attachments.MaxFileBytes}
	}
	if err != nil {
		return fail(err, name, "reading "+name+" failed")
	}
	if len(content) == 0 {
		return fail(nil, "", name+": the file is empty")
	}
	if _, err := mr.NextPart(); !errors.Is(err, io.EOF) {
		return fail(err, name, onePart)
	}
	return name, content, true
}

// planUploadFile serves an upload of the project to a viewer of its plans,
// read from GitLab with the viewer's own token: only an allowed type, as
// the type its content sniffs as, sandboxed, images and PDFs inline and text
// as a download, cached privately for an hour. The route's secret is never
// logged: the server logs no request paths, and GitLab's errors are not
// logged here.
func (s *Server) planUploadFile(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.loadPlans(w, r)
	if !ok {
		return
	}
	secret, filename := r.PathValue("secret"), r.PathValue("filename")
	if !uploadSecret.MatchString(secret) || attachments.CleanName(filename) != filename {
		notFound(w)
		return
	}
	gl, _, ok := s.userGitLab(w, r, sc.Project)
	if !ok {
		return
	}
	data, err := gl.DownloadUpload(r.Context(), sc.Project.Spec.GitLab.Project, secret, filename, attachments.MaxFileBytes)
	switch {
	case gitlab.IsNotFound(err):
		notFound(w)
		return
	case errors.Is(err, gitlab.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "the file is "+uploadTooLarge)
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "reading the file from GitLab failed, try again")
		return
	}
	ct, ok := attachments.Allowed(filename, data)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, filename+": "+attachments.Refusal(filename, data))
		return
	}
	disposition := "attachment"
	if attachments.Image(ct) || ct == "application/pdf" {
		disposition = "inline"
	}
	h := w.Header()
	// These replace the Desk's own security headers, set before the handler.
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	// The name is clean ASCII; filename* is the RFC 6266 form browsers prefer.
	h.Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, filename, url.PathEscape(filename)))
	h.Set("Cache-Control", "private, max-age=3600")
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
