/agent spec

## Goal

Todos can be organised with tags and due dates, and can be edited after
creation.

## Requirements

### Tags
- A todo has a list of tags (strings). `POST /todos` accepts an optional
  `tags` array.
- At most 5 tags per todo; a tag is 1–20 characters. Invalid input is a 400.
- `GET /todos?tag=<tag>` returns only todos with that tag.
- How tags are normalised (case, whitespace, duplicates) is not decided yet.

### Due dates
- A todo has an optional `due` date (`YYYY-MM-DD`). Invalid dates are a 400.
- `GET /todos?overdue=true` returns todos that are not done and whose due
  date is before today (server time).

### Editing
- `PATCH /todos/:id` updates `title`, `tags` and/or `due` with the same
  validation as on creation. Unknown fields are rejected.
- Returns the updated todo, 404 for unknown ids.

## Non-goals
- Persistence, authentication, pagination.
