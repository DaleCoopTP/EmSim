# EmSim web client

TypeScript/React SPA (RFC-001 §4.4), embedded into and served by the
`emsim api` process (`web/embed.go`, `cmd/emsim/static.go`) — see the root
[README.md](../README.md) for the full `docker compose up` flow.

```bash
npm ci
npm run dev     # http://localhost:5173, proxies /api to :8080 (start `go run ./cmd/emsim api` separately)
npm run check   # regenerate API types from ../design-docs/contracts/openapi.yaml, then tsc -b
npm run build   # -> dist/, embedded by web/embed.go's //go:embed
npm run test:e2e # isolated Chromium + compose browser acceptance test
```

`src/api/schema.d.ts` is generated (`npm run generate:api`, an
`openapi-typescript` wrapper) and gitignored — never edit it directly.
`dist/` is likewise gitignored except for a `.gitkeep` placeholder, so
`go build` works without Node ever having run; `make web-build` (or the
Dockerfile's Node stage) populates it.

The operator 112 intake has its own trainee workspace and instructor review
screens. Prepared questions now reveal only their spoken answers; hold and
resume preserve the server-side dialogue across reloads. The instructor
review shows the dialogue, disclosed facts, saved card changes and dispatch
snapshot. The existing incoming-call card still uses one training service
labelled 03. The three card-only 112-3 cases have a separate screen: selecting
an incident type adds profile 104, profile 101, or both, with blank answers.
The trainee saves the draft and reviews suggested training services; an
override requires a reason. This route has no caller simulation or dispatch.
See [slice-112-3-plan.md](../slice-112-3-plan.md).
The compact applicant row contains name, applicant status, and editable
telecom provider/channel. The entire address panel grows with its fields,
while short screens scroll the page. The card's training availability
indicator is local to the current browser and workstation: an open card
forces "unavailable" until ten seconds after closure, then restores the
manual choice. It does not report SIP connectivity.

`test:e2e` creates a uniquely named Compose project with its own volumes and
free localhost ports, feeds Chromium `seed/voice-assets/crew_leader_greeting.wav`
as a fake microphone, and removes that project after the test. Install the
browser once locally with:

```bash
PLAYWRIGHT_BROWSERS_PATH="$PWD/.playwright-browsers" npx playwright install chromium
```
