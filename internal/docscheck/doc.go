// Package docscheck keeps the documentation honest. Its test, run by
// `make test`, checks the repository's Markdown and the docs refs in code
// (see docs/specs/docs-revamp.spec.md, R2):
//
//   - relative links in Markdown must point to a file or directory that
//     exists, and an anchor into a Markdown page must match a heading (a
//     GitHub heading slug) or an explicit `<a id>`/`<a name>`;
//   - every `docs/….md` ref in a non-Markdown file must resolve, anchor
//     included;
//   - pages in the four user sections (getting-started, guides, reference,
//     concepts) and every README under docs/ have at most 400 lines;
//   - pages in the user sections name no source files or source directories;
//   - the entry points linked from outside the repository keep resolving.
//
// The package has no production code. The check lives in the _test.go
// files, so it adds nothing to the operator binary.
package docscheck
