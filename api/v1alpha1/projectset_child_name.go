package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jonathanwiemers/ai-operator/internal/naming"
)

// maxChildSlug is the length a child name's slug is cut to before the hash.
const maxChildSlug = 50

// ProjectSetChildName is the name of a set's child for a project: the slug of
// the path below the group, cut to 50 characters plus a hash of the full path
// when longer. The set controller and the Desk both use it, so the Desk names
// a dormant project's future child as the set will.
func ProjectSetChildName(group, path string) string {
	full := NormalizeProject(path)
	rel := full
	if g := NormalizeProject(group); g != "" {
		rel = strings.TrimPrefix(full, g+"/")
	}
	slug := naming.Slug(rel)
	if len(slug) <= maxChildSlug && slug != "" {
		return slug
	}
	sum := sha256.Sum256([]byte(full))
	slug = strings.Trim(slug[:min(len(slug), maxChildSlug)], "-")
	if slug == "" {
		slug = "project"
	}
	return slug + "-" + hex.EncodeToString(sum[:])[:6]
}
