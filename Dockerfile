# syntax=docker/dockerfile:1.9

FROM golang:1.27-alpine AS api-build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=development
ARG COMMIT=unknown
RUN --mount=type=cache,target=/root/.cache/go-build go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o /out/media-manager ./cmd/media-manager

FROM alpine:3.23 AS api
ARG VERSION=development
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="Media Manager API" \
      org.opencontainers.image.description="Media Manager owner API" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/Media-manager52/Media-manager"
RUN apk add --no-cache ca-certificates ffmpeg postgresql18-client tzdata
COPY --from=api-build /out/media-manager /usr/local/bin/media-manager
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/media-manager/
COPY third_party /usr/share/doc/media-manager/third_party
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=4s --start-period=15s --retries=4 CMD ["/usr/local/bin/media-manager", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/media-manager"]
CMD ["serve"]

FROM node:24-alpine AS ui-build
WORKDIR /web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
# CI checks out source with a private umask; the runtime user must read the build.
RUN npm run build && chmod -R a+rX /web/dist && chmod a+r /web/server.mjs

FROM ui-build AS ui-test
RUN npx vitest run

FROM node:24-alpine AS ui
ARG VERSION=development
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="Media Manager UI" \
      org.opencontainers.image.description="Media Manager owner console" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.source="https://github.com/Media-manager52/Media-manager"
WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOST=0.0.0.0
# The runtime is a dependency-free static server plus the built bundle; no
# node_modules, build tools or credentials are in the image.
COPY --from=ui-build /web/server.mjs ./server.mjs
COPY --from=ui-build /web/dist ./dist
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/doc/media-manager/
COPY third_party /usr/share/doc/media-manager/third_party
USER node
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=4s --start-period=10s --retries=4 CMD wget -q -O /dev/null http://127.0.0.1:3000/
CMD ["node", "server.mjs"]
