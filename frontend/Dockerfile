# syntax=docker/dockerfile:1.7
# Multi-stage build for the Vue 3 dashboard.
# Stage 1 runs `npm ci && npm run build` to produce `dist/`.
# Stage 2 serves the static files via nginx and proxies /api/* to the api service.

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . ./
RUN npm run build

FROM nginx:1.30.6-alpine@sha256:583b7f2fce1cdb8da5ad5c39ebd3c33b98b87d493190e548bdee1fa8c619c311
COPY --from=builder /src/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
