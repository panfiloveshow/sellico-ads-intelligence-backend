# Multi-stage build: install + bundle in node, ship as static via nginx-alpine.
#
# Build:
#   docker build -t sellico-frontend ./frontend
# Run (standalone, just to verify):
#   docker run --rm -p 8081:80 sellico-frontend
# In production this image is unused — the nginx container in the main
# docker-compose.prod.yml mounts the dist/ directory directly via volume.

ARG NODE_IMAGE=node:22-alpine
ARG NGINX_IMAGE=nginx:1.27-alpine

FROM ${NODE_IMAGE} AS build
WORKDIR /build
COPY package.json ./
# Use `npm install` instead of `ci` because we don't ship a lockfile in the
# initial scaffold; once a contributor runs `pnpm install` locally the lock
# can be added and this can switch to `pnpm install --frozen-lockfile`.
RUN npm install --no-audit --no-fund
COPY . .
RUN npm run build

FROM ${NGINX_IMAGE} AS runtime
COPY --from=build /build/dist /usr/share/nginx/html
COPY nginx-spa.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s CMD wget -q -O - http://127.0.0.1/ >/dev/null 2>&1 || exit 1
