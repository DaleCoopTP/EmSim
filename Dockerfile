# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM node:22-alpine@sha256:b6f26b36c8ff49624cfdac716b8ea1138d606df02586a77d364bb5536a634f85 AS web-build
WORKDIR /app
COPY web/package.json web/package-lock.json web/
RUN --mount=type=cache,target=/root/.npm cd web && npm ci
COPY web web/
COPY design-docs/contracts/openapi.yaml design-docs/contracts/openapi.yaml
RUN cd web && npm run build

FROM golang:1.26.6-alpine3.24@sha256:3889b425f035be855a72fb4755265311293b6d414521f0a519d819df32222d83 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
# internal/content/schema embeds scenario.schema.json/scenario-file.schema.json/
# rubric.default.json via design-docs/contracts/embed.go (slice 2's C1) —
# .dockerignore lets only *.json and embed.go through this directory, so
# this is not the whole design-docs/contracts tree (openapi.yaml, check.py,
# schema.sql, ... stay out of the build context here).
COPY design-docs/contracts design-docs/contracts
COPY web/embed.go web/embed.go
COPY --from=web-build /app/web/dist web/dist
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/emsim ./cmd/emsim

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS runtime
WORKDIR /app
USER 65532:65532
COPY --from=build /out/emsim /app/emsim
# seed/ (slice 2's C3 pilot catalogue) is read by "emsim import seed
# --actor ... /app/seed" — compose.yaml's one-shot "seed" service.
COPY seed /app/seed
RUN mkdir /app/blobs && chown 65532:65532 /app/blobs
ENTRYPOINT ["/app/emsim"]
