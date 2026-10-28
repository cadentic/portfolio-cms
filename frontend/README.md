# Web (Angular 22 SSR)

Public site, owner console (`/admin`) and the Node server that renders pages
and proxies `/api`, `/mcp` and `/oauth` to the Go API. See the root README for
running the whole stack.

```sh
npm ci
npm run build                     # dist/frontend
API_INTERNAL_URL=http://localhost:8080 node dist/frontend/server/server.mjs
```

Server environment variables are listed in `docs/configuration.md`.
