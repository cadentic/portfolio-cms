# syntax=docker/dockerfile:1.7
# Angular SSR web server: renders public pages, serves the admin app and
# proxies /api, /mcp and /oauth to the Go API (API_INTERNAL_URL).
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production PORT=4000
# The server bundle includes its dependencies, so only dist/ is needed.
COPY --from=build --chown=node:node /app/dist/frontend ./dist/frontend
USER node
EXPOSE 4000
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s CMD wget -qO- http://127.0.0.1:4000/healthz >/dev/null || exit 1
CMD ["node", "dist/frontend/server/server.mjs"]
