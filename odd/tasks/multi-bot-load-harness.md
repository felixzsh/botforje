# Multi-bot integration and capacity harness

Feature branch `test/multi-bot-load-harness`. Plan and boundaries:
`docs/channels/load-testing-plan.md`. Code lives in a test-only `loadtest`
package plus the synthetic adapter's bounded-driving support.

## Specs

S1 — Integration tests that mock an adapter. User request: "hacer test
d eintegracion mas completos tipo mockeando un adaptar e". Must drive the
real config, fleet, engine, SQLite stores, and outbox through the synthetic
channel, with exact correlated assertions rather than aggregate counts.

S2 — Hundreds of simultaneous parallel adapter instances. User request:
"cubrir escenarios con cientos de adaptares simultaneos paralelos simulando
ser canales de mensajeria y ver que todo funcione bien". Many bot instances
run in one fleet and one process; each expected reply is observed exactly
once and never crosses bots or scopes.

S3 — Profiling and resource behavior. User request: "hacer profiling o ma
squ enada ver si lo soporta bien razonablemente en cuanto a recursos".
Runs report throughput, latency, CPU, memory, goroutines, and pending work,
and can emit Go profiles for diagnosis.

S4 — Adjustable instance count. User request: "que sea facil twkear aumentar
la cantidad de instancias para yo ir aumentando poco a poco". Bot count,
chats, rate, duration, payload, and seed are inputs of one opt-in run, so
the operator can step the load up without editing code.

S5 — A capacity estimate. User request: "al final saber mas o menos cuantas
instancias este motor botflota pueda manejar razonablemente bien en una sla
instancia de botflota". Evidence is reported as an operating envelope tied
to named hardware, workload, and pacing, never as a universal maximum.

## Tasks

| ID | Specs | Route | Commit | State |
| --- | --- | --- | --- | --- |
| T1 | S3 | inline | | pending |
| T2 | S1, S2 | inline | | pending |
| T3 | S2, S3, S4 | inline | | pending |
| T4 | S3, S4 | inline | | pending |
| T5 | S5 | inline | | pending |

T1 — Cancellable inbound injection and bounded observation in the synthetic
adapter, keeping the existing conformance suite green.

T2 — Multi-bot harness and functional scenarios: concurrent correlated
replies, scope/bot isolation, redelivery, and shutdown under load.

T3 — Opt-in capacity runner: open-loop generation, startup/warm-up/measure/
drain phases, bounded in-flight correlation, honest overload accounting, and
a summary plus JSON evidence.

T4 — Profiles and operator surface: optional CPU/heap profiles, documented
env knobs, and a Make target that stays out of `make check`.

T5 — Execute a baseline and incremental sweep on this machine and record the
operating envelope and its limits in this document.

## Log

L1 — User request, verbatim: "teniendo en cuenta se que acab ade implementar
todo el @docs/channels/plan.md ahora lo que me interesa es hacer aprovechar
est abase para hacer test d eintegracion mas completos tipo mockeando un
adaptar e incluso cubrir escenarios con cientos de adaptares simultaneos
paralelos simulando ser canales de mensajeria y ver que todo funcione bien e
incluso tipo hacer profiling o ma squ enada ver si lo soporta bien
razonablemente en cuanto a recursos y que sea facil twkear aumentar la
cantidad de instancias para yo ir aumentando poco a poco para al final saber
mas o menos cuantas instancias este motor botflota pueda manejar
razonablemente bien en una sla instancia de botflota".

L2 — User request, verbatim: "por ahora deja definido el plan". Result:
`docs/channels/load-testing-plan.md` created; no code changed.

L3 — User request, verbatim: "puedes empezar a orquestarlo". Authorizes the
implementation in the plan document.

L4 — Evidence: `make check` on `a960a55` passed before the first change
(vet, gofmt, line check, full test suite).

L5 — Scope decision: the synthetic adapter is not extended with send-failure
injection. Outbox retry and permanent-failure semantics are already pinned by
`fleet/outbox_test.go`; duplicating them at scale would add test-only
machinery without new coverage. The slow/degraded-channel item of the plan
stays documented as a follow-up, not silently claimed.
