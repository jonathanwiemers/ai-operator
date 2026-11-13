# Contributing

Thanks for looking. ai-operator is developed on GitLab:
<https://gitlab.com/jo.wiemers/ai-operator>. GitHub
(<https://github.com/jonathanwiemers/ai-operator>) is a read-only mirror of
`main` and the place for releases and security advisories.

## Reporting bugs and ideas

Open an issue on GitHub. Include the version (`YEAR.MONTH.N`), what you did,
what you expected and what happened. Logs of the operator or the agent pod
help; remove tokens and private content first.

Security problems: please don't open an issue, see [SECURITY.md](SECURITY.md).

## Changes

1. For anything bigger than a small fix, open an issue first so we can agree
   on the approach. Larger features start with a spec in `docs/specs/`.
2. Keep merge requests small and focused. Docs in `docs/` and the chart
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

Merge requests are reviewed on GitLab; a pull request on GitHub is fine too,
it is moved over by hand.

## License

By contributing you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
