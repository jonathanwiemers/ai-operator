package ui

import "time"

// gitlabTokenTTL is how long a GitLab connection is kept: the refresh token
// renews the access token (GitLab's last two hours) until then.
const gitlabTokenTTL = 30 * 24 * time.Hour

// GitLabToken is the content of the GitLab connection cookie. It belongs to
// the UI user who connected it and is never sent to the app's JavaScript
// (so it is in its own file, which tygo.yaml leaves out of the web types).
type GitLabToken struct {
	Owner    string `json:"owner"`
	Access   string `json:"at"`
	Refresh  string `json:"rt"`
	Expires  int64  `json:"exp"`
	Username string `json:"user"`
	UserID   int64  `json:"uid"`
	// Until ends the connection, however often the token is refreshed.
	Until int64 `json:"until"`
	// Fixed: the configured token of gitlab.tokenUser, not an OAuth token.
	Fixed bool `json:"-"`
}
