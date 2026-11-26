package attachments

import (
	"reflect"
	"strings"
	"testing"
)

const (
	sec  = "0123456789abcdef0123456789abcdef"
	sec2 = "fedcba9876543210fedcba9876543210"
)

var proj = Project{URL: "https://gitlab.example.com/", Path: "group/app", ID: 42}

func TestFind(t *testing.T) {
	const (
		rel = "/uploads/" + sec + "/shot.png"
		abs = "https://gitlab.example.com/group/app/uploads/" + sec + "/shot.png"
		id  = "https://gitlab.example.com/-/project/42/uploads/" + sec + "/shot.png"
	)
	one := func(raw string, image bool) []Link {
		return []Link{{Raw: raw, Secret: sec, Filename: "shot.png", Image: image}}
	}
	for name, c := range map[string]struct {
		text string
		p    Project
		want []Link
	}{
		"relative image":       {"![shot](" + rel + ")", proj, one(rel, true)},
		"relative link":        {"see [shot](" + rel + ")", proj, one(rel, false)},
		"relative with title":  {`![shot](` + rel + ` "the header")`, proj, one(rel, true)},
		"relative bare":        {"see " + rel, proj, nil},
		"path image":           {"![shot](" + abs + ")", proj, one(abs, true)},
		"path link":            {"[shot](" + abs + ")", proj, one(abs, false)},
		"path bare":            {"look at " + abs + " please", proj, one(abs, false)},
		"bare, end of phrase":  {"look at " + abs + ".", proj, one(abs, false)},
		"bare in angle quotes": {"<" + abs + ">", proj, one(abs, false)},
		"id image":             {"![shot](" + id + ")", proj, one(id, true)},
		"id bare":              {id, proj, one(id, false)},
		"id unknown":           {"![shot](" + id + ")", Project{URL: proj.URL, Path: proj.Path}, nil},
		"no gitlab url":        {abs + " " + id + " ![a](" + rel + ")", Project{}, one(rel, true)},
		"no path":              {abs, Project{URL: proj.URL, ID: 42}, nil},
		"case of host and path": {
			"![a](HTTPS://GitLab.Example.com/Group/App/Uploads/" + sec + "/shot.png)", proj,
			one("HTTPS://GitLab.Example.com/Group/App/Uploads/"+sec+"/shot.png", true),
		},
		"uppercase hex": {
			"[a](/uploads/" + strings.ToUpper(sec) + "/shot.png)", proj,
			one("/uploads/"+strings.ToUpper(sec)+"/shot.png", false),
		},
		"encoded name": {
			"[a](/uploads/" + sec + "/my%20shot%281%29.png)", proj,
			[]Link{{Raw: "/uploads/" + sec + "/my%20shot%281%29.png", Secret: sec, Filename: "my shot(1).png"}},
		},
		"other host":            {"https://gitlab.com/group/app/uploads/" + sec + "/shot.png", proj, nil},
		"other host same path":  {"https://evil.example/gitlab.example.com/group/app/uploads/" + sec + "/shot.png", proj, nil},
		"host prefix":           {"https://gitlab.example.com.evil/group/app/uploads/" + sec + "/shot.png", proj, nil},
		"other scheme":          {"http://gitlab.example.com/group/app/uploads/" + sec + "/shot.png", proj, nil},
		"other project":         {"https://gitlab.example.com/group/other/uploads/" + sec + "/shot.png", proj, nil},
		"sub project":           {"https://gitlab.example.com/group/app/sub/uploads/" + sec + "/shot.png", proj, nil},
		"other id":              {"https://gitlab.example.com/-/project/43/uploads/" + sec + "/shot.png", proj, nil},
		"id prefix":             {"https://gitlab.example.com/-/project/420/uploads/" + sec + "/shot.png", proj, nil},
		"secret 31":             {"![a](/uploads/" + sec[:31] + "/shot.png)", proj, nil},
		"secret 33":             {"![a](/uploads/" + sec + "0/shot.png)", proj, nil},
		"secret not hex":        {"![a](/uploads/" + sec[:31] + "g/shot.png)", proj, nil},
		"no file":               {"![a](/uploads/" + sec + "/)", proj, nil},
		"no file segment":       {"![a](/uploads/" + sec + ")", proj, nil},
		"extra segment":         {"![a](/uploads/" + sec + "/a/shot.png)", proj, nil},
		"trailing slash":        {"![a](/uploads/" + sec + "/shot.png/)", proj, nil},
		"query":                 {"![a](/uploads/" + sec + "/shot.png?x=1)", proj, nil},
		"fragment":              {abs + "#top", proj, nil},
		"dotdot":                {"![a](/uploads/" + sec + "/../x.png)", proj, nil},
		"dotdot as secret":      {"![a](/uploads/../" + sec + "/x.png)", proj, nil},
		"encoded dotdot":        {"![a](/uploads/" + sec + "/%2e%2e)", proj, nil},
		"encoded dot":           {"![a](/uploads/" + sec + "/%2E)", proj, nil},
		"encoded slash":         {"![a](/uploads/" + sec + "/..%2Fx.png)", proj, nil},
		"encoded backslash":     {"![a](/uploads/" + sec + "/..%5Cx.png)", proj, nil},
		"backslash":             {`![a](/uploads/` + sec + `/..\x.png)`, proj, nil},
		"bad escape":            {"![a](/uploads/" + sec + "/a%zz.png)", proj, nil},
		"relative in other dir": {"![a](/other/uploads/" + sec + "/shot.png)", proj, nil},
		"no links":              {"just text, https://example.com", proj, nil},
	} {
		if got := Find(c.text, c.p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Find = %+v, want %+v", name, got, c.want)
		}
	}
}

func TestFindOrderAndDuplicates(t *testing.T) {
	text := "First [log](/uploads/" + sec2 + "/app.log), then\n" +
		"![shot](https://gitlab.example.com/group/app/uploads/" + sec + "/shot.png)\n" +
		"again ![shot](/uploads/" + sec + "/shot.png) and https://gitlab.example.com/-/project/42/uploads/" + strings.ToUpper(sec) + "/shot.png\n" +
		"same secret, other name: /uploads/" + sec + "/other.png and [o](/uploads/" + sec + "/other.png)"
	got := Find(text, proj)
	want := []Link{
		{Raw: "/uploads/" + sec2 + "/app.log", Secret: sec2, Filename: "app.log"},
		{Raw: "https://gitlab.example.com/group/app/uploads/" + sec + "/shot.png", Secret: sec, Filename: "shot.png", Image: true},
		{Raw: "/uploads/" + sec + "/other.png", Secret: sec, Filename: "other.png"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Find = %+v\nwant %+v", got, want)
	}
}
