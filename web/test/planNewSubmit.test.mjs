import assert from "node:assert/strict";
import { test } from "node:test";
import { startPlan } from "../src/planNewSubmit.ts";

const f = (name) => new File(["x"], name);

test("uploads in order before create, and the body carries the lines", async () => {
  const log = [];
  const r = await startPlan({
    files: [f("a.png"), f("b.log")],
    message: "hello ",
    title: " T ",
    upload: async (x) => (
      log.push("up " + x.name),
      { markdown: `![${x.name}](/uploads/s/${x.name})` }
    ),
    create: async (b) => (log.push("create"), { b }),
  });
  assert.deepEqual(log, ["up a.png", "up b.log", "create"]);
  assert.deepEqual(r.plan.b, {
    message: "hello\n![a.png](/uploads/s/a.png)\n![b.log](/uploads/s/b.log)",
    title: "T",
  });
});

test("a failed upload names the file and never creates", async () => {
  let created = false;
  const r = await startPlan({
    files: [f("a.png"), f("bad.pdf")],
    message: "m",
    title: "",
    upload: async (x) => {
      if (x.name === "bad.pdf")
        throw Object.assign(new Error("boom"), { status: 400 });
      return { markdown: "x" };
    },
    create: async () => ((created = true), {}),
  });
  assert.equal(created, false);
  assert.match(r.error, /bad\.pdf/);
});

test("a 409 is passed on, and without files the message is untouched", async () => {
  const r = await startPlan({
    files: [f("a.png")],
    message: "m",
    title: "",
    upload: async () => {
      throw Object.assign(new Error("x"), { status: 409 });
    },
    create: async () => ({}),
  });
  assert.equal(r.status, 409);
  const p = await startPlan({
    files: [],
    message: " m ",
    title: "",
    upload: async () => ({ markdown: "" }),
    create: async (b) => b,
  });
  assert.deepEqual(p.plan, { message: " m " });
});
