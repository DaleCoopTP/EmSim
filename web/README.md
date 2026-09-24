# EmSim web client

TypeScript/React SPA (RFC-001 §4.4), embedded into and served by the
`emsim api` process (`web/embed.go`, `cmd/emsim/static.go`) — see the root
[README.md](../README.md) for the full `docker compose up` flow.

```bash
npm ci
npm run dev     # http://localhost:5173, proxies /api to :8080 (start `go run ./cmd/emsim api` separately)
npm run check   # regenerate API types from ../design-docs/contracts/openapi.yaml, then tsc -b
npm run build   # -> dist/, embedded by web/embed.go's //go:embed
npm run test:e2e # isolated Chromium + compose browser acceptance tests (DDS, 112 incoming-call, 112 card_only, 112 full_case)
```

`src/api/schema.d.ts` is generated (`npm run generate:api`, an
`openapi-typescript` wrapper) and gitignored — never edit it directly.
`dist/` is likewise gitignored except for a `.gitkeep` placeholder, so
`go build` works without Node ever having run; `make web-build` (or the
Dockerfile's Node stage) populates it.

The operator 112 intake has its own trainee workspace and instructor review
screens. Prepared questions now reveal only their spoken answers; hold and
resume preserve the server-side dialogue across reloads. The pre-slice
incoming-call route (`Operator112IncomingWorkplace`, one training service
labelled 03, `dispatch_intake`) is untouched and still used by scenarios
that predate the card_only/full_case split. See
[slice-112-3-plan.md](../slice-112-3-plan.md).
The `card_only` and `full_case` (112-4) routes share one component
(`Operator112ProfileCase.tsx`) on the ARM-112 layout from the operator
instruction (`docs/reference-ui/112-instruction/image37.png`): phone strip
with the elapsed timer, applicant row, address and description on the left,
incident type search with the per-service maps on the right, and the orange
services bar. Selecting an incident type adds profile 104, profile 101, or
both, with blank answers. `full_case` additionally starts as a ringing call —
the trainee answers, asks prepared questions (their own transcript/questions
panel), then adds the incident type(s) and cards the same way `card_only`
does; "нет контакта"/"срыв звонка" close the card without a notification,
same as the incoming-call route. Controls the simulator does not model (SMS,
call records, reminders, links) are shown disabled.
Items created for this slice on carry `intake_state.finale=notify`: after
saving the draft, the bar's «+» opens «Список оповещаемых служб» with the
rule-suggested services already checked (manual changes require a reason),
and «оповестить и сохранить карточку» (`notify_services`) writes one
immutable notification record and locks the card; «завершить»
(`complete_intake`) needs that notification (and, for `full_case`, the call
ended). Items already open before this slice shipped have no `finale` and
keep the prior modal/labels and `review_service_selection`/
`complete_profile_case` route unchanged. × on the bar returns to the case
list either way. See [slice-112-4-plan.md](../slice-112-4-plan.md) and
[ADR-023](../design-docs/adr/023-operator112-notify-and-save.md).
The instructor review (`ItemReview.tsx`) shows the dialogue transcript for
`full_case`/incoming-call routes, the profile cards and action log for
`card_only`/`full_case`, and either the notification record (recipients,
suggested-vs-manual, reason, time) or the legacy dispatch/service-review
snapshot, whichever the item actually produced. The monitor
(`Monitor.tsx`) row for an active 112 item shows the incident type once
chosen and "· оповещено" once notified, alongside the call state.
An assignment can contain several ordered 112 cases. The next case is offered
when the current one closes, and the trainee can return to the case list after
finishing the last case while the workplace remains open.
The assignment editor preselects the next available case after an addition and
can append cases to an already saved queue before the lesson starts.
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
