# EmSim web client

TypeScript/React SPA (RFC-001 §4.4), embedded into and served by the
`emsim api` process (`web/embed.go`, `cmd/emsim/static.go`) — see the root
[README.md](../README.md) for the full `docker compose up` flow.

```bash
npm ci
npm run dev     # http://localhost:5173, proxies /api to :8080 (start `go run ./cmd/emsim api` separately)
npm run check   # regenerate API types from ../design-docs/contracts/openapi.yaml, then tsc -b
npm run build   # -> dist/, embedded by web/embed.go's //go:embed
```

`src/api/schema.d.ts` is generated (`npm run generate:api`, an
`openapi-typescript` wrapper) and gitignored — never edit it directly.
`dist/` is likewise gitignored except for a `.gitkeep` placeholder, so
`go build` works without Node ever having run; `make web-build` (or the
Dockerfile's Node stage) populates it.

Visual styling follows ADR-009's ARM-112 pass, scheduled after slice 7
(slice-planning.md §1) — this scaffold and slice 1's own screens are
intentionally unstyled.
