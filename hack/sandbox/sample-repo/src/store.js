// In-memory todo store.
export class TodoStore {
  #todos = new Map();
  #nextId = 1;

  add(title) {
    if (typeof title !== "string" || title.trim() === "") {
      throw new ValidationError("title is required");
    }
    const todo = { id: this.#nextId++, title: title.trim(), done: false };
    this.#todos.set(todo.id, todo);
    return todo;
  }

  get(id) {
    return this.#todos.get(id);
  }

  list() {
    return [...this.#todos.values()];
  }

  complete(id) {
    const todo = this.#todos.get(id);
    if (todo) {
      todo.done = true;
    }
    return todo;
  }

  remove(id) {
    return this.#todos.delete(id);
  }
}

export class ValidationError extends Error {}
