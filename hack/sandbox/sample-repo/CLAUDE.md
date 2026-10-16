# sandbox-todo

A tiny todo API without dependencies (Node 22, ES modules).

- Do not add npm dependencies. Use the Node standard library only.
- Keep `src/store.js` free of HTTP concerns; `src/server.js` maps HTTP to the store.
- Every change needs tests in `test/` (`node:test` + `node:assert/strict`).
- Run `npm test` before finishing; it must pass.
- Validation errors are `ValidationError` in the store and HTTP 400 in the server.
