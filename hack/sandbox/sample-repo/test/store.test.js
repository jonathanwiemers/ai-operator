import { test } from "node:test";
import assert from "node:assert/strict";
import { TodoStore, ValidationError } from "../src/store.js";

test("add trims the title and assigns ids", () => {
  const store = new TodoStore();
  assert.deepEqual(store.add("  write tests "), { id: 1, title: "write tests", done: false });
  assert.equal(store.add("second").id, 2);
});

test("add rejects empty titles", () => {
  assert.throws(() => new TodoStore().add("   "), ValidationError);
});

test("complete and remove", () => {
  const store = new TodoStore();
  const { id } = store.add("x");
  assert.equal(store.complete(id).done, true);
  assert.equal(store.complete(99), undefined);
  assert.equal(store.remove(id), true);
  assert.deepEqual(store.list(), []);
});
