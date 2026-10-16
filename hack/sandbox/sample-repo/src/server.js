import http from "node:http";
import { ValidationError } from "./store.js";

// createServer exposes a TodoStore over HTTP:
//   GET    /todos                list todos
//   POST   /todos                {"title": "..."} -> 201 + todo
//   POST   /todos/:id/complete   mark done
//   DELETE /todos/:id            remove
export function createServer(store) {
  return http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, "http://localhost");
      const parts = url.pathname.split("/").filter(Boolean);

      if (parts[0] !== "todos") {
        return send(res, 404, { error: "not found" });
      }
      if (parts.length === 1 && req.method === "GET") {
        return send(res, 200, store.list());
      }
      if (parts.length === 1 && req.method === "POST") {
        const body = await readJSON(req);
        return send(res, 201, store.add(body.title));
      }

      const id = Number(parts[1]);
      if (!Number.isInteger(id)) {
        return send(res, 400, { error: "invalid id" });
      }
      if (parts.length === 3 && parts[2] === "complete" && req.method === "POST") {
        const todo = store.complete(id);
        return todo ? send(res, 200, todo) : send(res, 404, { error: "not found" });
      }
      if (parts.length === 2 && req.method === "DELETE") {
        return store.remove(id) ? send(res, 204) : send(res, 404, { error: "not found" });
      }
      return send(res, 405, { error: "method not allowed" });
    } catch (err) {
      if (err instanceof ValidationError || err instanceof SyntaxError) {
        return send(res, 400, { error: err.message });
      }
      return send(res, 500, { error: "internal error" });
    }
  });
}

async function readJSON(req) {
  let raw = "";
  for await (const chunk of req) {
    raw += chunk;
  }
  return raw ? JSON.parse(raw) : {};
}

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" });
  res.end(body === undefined ? "" : JSON.stringify(body));
}
