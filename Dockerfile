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
RUN apk add --no-cache ca-certificates ffmpeg postgresql18-client tzdata
COPY --from=api-build /out/media-manager /usr/local/bin/media-manager
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=4s --start-period=15s --retries=4 CMD ["/usr/local/bin/media-manager", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/media-manager"]
CMD ["serve"]

FROM node:24-alpine AS ui-build
WORKDIR /app
RUN apk add --no-cache bash coreutils
COPY package.json package-lock.json .npmrc ./
COPY scripts ./scripts
RUN --mount=type=cache,target=/root/.npm npm ci
COPY app ./app
COPY build ./build
COPY components ./components
COPY hooks ./hooks
COPY lib ./lib
COPY public ./public
COPY third_party ./third_party
COPY worker ./worker
COPY .openai ./.openai
COPY cloudflare-env.d.ts components.json eslint.config.mjs next.config.ts postcss.config.mjs tsconfig.json vite.config.ts ./
ARG NEXT_PUBLIC_MEDIA_MANAGER_MODE=live
ENV NEXT_PUBLIC_MEDIA_MANAGER_MODE=${NEXT_PUBLIC_MEDIA_MANAGER_MODE}
RUN npm run build

FROM ui-build AS ui-test
COPY tests ./tests
RUN node --test tests/*.test.mjs

FROM node:24-alpine AS ui
WORKDIR /app
ENV NODE_ENV=production PORT=3000
# The built bundle targets Workers and imports cloudflare:workers for optional
# tracing; the Node runtime loads an inert module for that scheme.
ENV NODE_OPTIONS=--import=/app/scripts/register-cloudflare-loader.mjs
COPY --from=ui-build /app/package.json /app/package-lock.json ./
COPY --from=ui-build /app/node_modules ./node_modules
COPY --from=ui-build /app/dist ./dist
COPY --from=ui-build /app/scripts ./scripts
COPY --from=ui-build /app/.openai ./.openai
COPY --from=ui-build /app/build ./build
COPY --from=ui-build /app/vite.config.ts /app/next.config.ts /app/tsconfig.json ./
RUN mkdir -p /app/.wrangler && chown node:node /app/.wrangler
USER node
EXPOSE 3000
HEALTHCHECK --interval=15s --timeout=4s --start-period=15s --retries=4 CMD wget -q -O /dev/null http://127.0.0.1:3000/
CMD ["npm", "run", "start", "--", "--hostname", "0.0.0.0", "--port", "3000"]
