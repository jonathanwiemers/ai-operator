# Contributing

Thanks for looking. Issues, pull requests, releases and security advisories
live on GitHub: <https://github.com/jonathanwiemers/ai-operator>.

The work happens in GitLab, where the operator's own agents build
ai-operator. Everything is mirrored to this repository: every commit on
`main`, every release tag and its release. That is why commits on `main` can
arrive in batches.

## Reporting bugs and ideas

Open an issue on GitHub. Include the version (`YEAR.MONTH.N`), what you did,
what you expected and what happened. Logs of the operator or the agent pod
help; remove tokens and private content first.

Security problems: please don't open an issue, see [SECURITY.md](SECURITY.md).

## Changes

1. For anything bigger than a small fix, open an issue first so we can agree
   on the approach. Larger features start with a spec in `docs/specs/`.
2. Keep pull requests small and focused. Docs in `docs/` and the chart
   README change together with the behaviour they describe.
3. Before pushing:

   ```sh
   gofmt -l .                # must print nothing
   go vet ./...
   make test
   make helm-lint
   make ui                   # only when web/ changed
   ```

   After changing `api/` types or kubebuilder markers run
   `make generate manifests`; after changing UI view structs run
   `make ts-types`. CI fails on stale generated files.
4. One topic per commit, with a short subject that says what changes.

Pull requests are reviewed here. A merged pull request is applied in the
GitLab project with you as the commit author, and comes back to `main` with
the next push; the pull request is then closed with a link to that commit.

## License

By contributing you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
