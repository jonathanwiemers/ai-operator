import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "../src/server.js";
import { TodoStore } from "../src/store.js";

let server;
let base;

before(async () => {
  server = createServer(new TodoStore());
  await new Promise((resolve) => server.listen(0, resolve));
  base = `http://127.0.0.1:${server.address().port}`;
});

after(() => server.close());

test("create, list, complete and delete a todo", async () => {
  let res = await fetch(`${base}/todos`, { method: "POST", body: JSON.stringify({ title: "ship it" }) });
  assert.equal(res.status, 201);
  const todo = await res.json();

  res = await fetch(`${base}/todos`);
  assert.deepEqual(await res.json(), [todo]);

  res = await fetch(`${base}/todos/${todo.id}/complete`, { method: "POST" });
  assert.equal((await res.json()).done, true);

  res = await fetch(`${base}/todos/${todo.id}`, { method: "DELETE" });
  assert.equal(res.status, 204);
});

test("validation errors are 400", async () => {
  const res = await fetch(`${base}/todos`, { method: "POST", body: JSON.stringify({ title: "" }) });
  assert.equal(res.status, 400);
});
