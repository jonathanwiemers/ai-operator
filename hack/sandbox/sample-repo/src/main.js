import { createServer } from "./server.js";
import { TodoStore } from "./store.js";

const port = Number(process.env.PORT ?? 3000);
createServer(new TodoStore()).listen(port, () => {
  console.log(`listening on :${port}`);
});
