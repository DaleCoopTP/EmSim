# EmSim web client

TypeScript/React SPA (RFC-001 §4.4), embedded into and served by the
`emsim api` process (`web/embed.go`, `cmd/emsim/static.go`) — see the root
[README.md](../README.md) for the full `docker compose up` flow.

```bash
npm ci
npm run dev     # http://localhost:5173, proxies /api to :8080 (start `go run ./cmd/emsim api` separately)
npm run check   # regenerate API types from ../design-docs/contracts/openapi.yaml, then tsc -b
npm run build   # -> dist/, embedded by web/embed.go's //go:embed
npm run test:e2e # isolated Chromium + compose browser acceptance tests (DDS, 112 incoming-call, 112 card_only, 112 full_case, 112 free-text caller chat)
```

`src/api/schema.d.ts` is generated (`npm run generate:api`, an
`openapi-typescript` wrapper) and gitignored — never edit it directly.
`dist/` is likewise gitignored except for a `.gitkeep` placeholder, so
`go build` works without Node ever having run; `make web-build` (or the
Dockerfile's Node stage) populates it.

The DDS workplace (`routes/trainee/Workplace.tsx`) works a card the way the
DDS guide describes (ADR-030): after "Открыть карточку", the trainee's own
service in the card's service list becomes a live block
(`ServiceStatusBlock`, passed to `IncidentCard` as `mineSlot`) with the
current reaction status, a ▾ history of saved statuses and comments, and
the ✎ pencil — a select built from `allowed_transitions`, a comment, and
"Сохранить". A status in `terminal_statuses` warns that saving closes the
card, and the server closes it. The queue, the card header and the
instructor monitor show the derived `card_status` (red for «Не оповещено»,
«Отказ», «Не завершено»); the instructor review lists the saved statuses
with comments. The old accept/reject/comment/close buttons and the okrug
form stay only for items whose `terminal_statuses` is empty (the archived
slice 2–7 pilots).

ДДС-2 (ADR-031) puts `CrewCommsPanel` («Связь с бригадой») beside the card
for those items. It holds:
- the ringing `incoming_call` banner with a countdown and «Ответить»
  (`answer_incoming`);
- the answered call's words and «Завершить разговор» (`call_end` without a
  call log);
- the phone, its contacts grouped by `role`, showing a phrase's text when
  there is no recording;
- one time-ordered log of reports and calls, with missed calls in red.

The instructor monitor's «Связь» column reads `rows[].reports`: the latest
report, its age and the actual reaction delay. The review screen
(`DDSCommsReview`) lists every report and call with its text, answer and
reaction times.

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
snapshot, whichever the item actually produced. For a `caller_mode:
"free_text"` case (112-5a) it also lists each caller-chat turn's status
(answered with its adapter, no reply for a technical reason, cancelled by
hold/end/dropped call, or still pending at stop) under its own "Ходы
свободного диалога" heading, and the action log labels
`send_caller_message`. The monitor (`Monitor.tsx`) row for an active 112
item shows the incident type once chosen and "· оповещено" once notified,
alongside the call state, and labels `send_caller_message` as the last
action the same way.
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

For `full_case` items with `intake_state.caller_mode: "free_text"`
(112-5a, [ADR-024](../design-docs/adr/024-operator112-async-caller-reply.md)),
`CallerChat.tsx` replaces the prepared-questions transcript panel with a
floating chat window (collapsible to a launcher button with an unread badge;
open/collapsed persists per item in localStorage). The operator types
free-text messages; the caller's reply arrives asynchronously (a worker task,
not part of the command's own transaction) and never bumps `item.seq`, so an
unsaved card draft survives it — the card's own `useEffect` resets the draft
off `JSON.stringify(item.card)`, not `item.seq`, specifically because every
accepted command (including a chat message) bumps the latter. While a reply
is pending the window shows "Заявитель печатает…"; hold, end-call, or a
dropped/no-contact close cancel that pending turn instead of leaving it
stuck. This slice's replies come from a deterministic six-phrase stub
(`adapter: "stub/v1"`); 112-5b swaps only the `CallerReplier` port for a real
model.

`test:e2e` creates a uniquely named Compose project with its own volumes and
free localhost ports, feeds Chromium `seed/voice-assets/crew_leader_greeting.wav`
as a fake microphone, and removes that project after the test. Install the
browser once locally with:

```bash
PLAYWRIGHT_BROWSERS_PATH="$PWD/.playwright-browsers" npx playwright install chromium
```
