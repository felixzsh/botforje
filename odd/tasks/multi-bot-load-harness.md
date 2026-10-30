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
| T1 | S3 | inline | c56879f | done |
| T2 | S1, S2 | inline | 0ef0daf | done |
| T3 | S2, S3, S4 | inline | 0c71b13 | done |
| T4 | S3, S4 | delegated | e3a5cbc | done |
| T5 | S5 | inline | 02d8719 | done |

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

L6 — Finding while running the 48-bot burst under `-race`: concurrent turns can
fail with `SQLITE_BUSY` (peer, chat, or session writes). Every store uses
`BeginTx(ctx, nil)`, a deferred transaction, and a read-then-write upgrade can
return busy without honoring `busy_timeout`. A failed turn is reported, stores
nothing, and leaves the receive cursor unconfirmed, so it stays replayable.
Decision: not fixed here. Changing transaction mode (`BEGIN IMMEDIATE`) is a
store-semantics change outside this harness and remains an owner decision.

L7 — Finding: a reply whose send succeeded but whose provider id could not be
recorded is retried, so the same synthetic input can be delivered twice. This
is the documented at-least-once behavior (channels plan), not a new defect. The
functional suite therefore asserts exactly-once only below the contention
regime, and the burst test asserts correlated ownership, no silent loss, and
duplicates only alongside a reported failure.

L8 — Evidence correction: `runtime/metrics` `/cpu/classes/total:cpu-seconds`
reported zero in this environment, so process CPU is read from `getrusage`
(with a portable fallback) instead of an unusable metric.

L9 — Review disposition: native assess over the committed range `a960a55..HEAD`
reported `nativeRisk: medium` (executable change in the synthetic adapter) and
`reviewDue: false` with reason `under_budget`, so no review lineage was started.
The work-unit commits stay independently reviewable; the accumulated branch is
not a review candidate.

L10 — Experiment: `_txlock=immediate` in a scratch copy changed nothing
(99.6% completed at 100 bots, 49 failed turns, ~95 turns/s), so the busy errors
are not a deferred-transaction upgrade. Raising `busy_timeout` from 5s to 30s
in a second scratch copy removed every failure at the same offered load
(3000/3000 completed, ~96.8 turns/s), at the cost of exposing the write queue
as latency (p50 1.3s, p95 9.5s). The 5s timeout decides whether overload
surfaces as failed turns or as queueing, not how much work fits.

L11 — Experiment: capping the SQLite pool at 8 connections with 500 bots cut
peak RSS from 994MB to 96MB, removed all failed turns, and raised completed
turns/s from 14.8 to 101.9. The 1GB peak was the unlimited connection pool, not
the Go heap (peak 34MB). Both fixes are production adapter settings, not test
code; adopting them is an owner decision recorded in the plan document, not
applied on this branch.

L12 — Measured envelope on this machine (2026-10-30, 8 vCPU, 15.6GB, btrfs):
50 turns/s is comfortable (p95 160ms on disk, one core, 40MB RSS); the write
path plateaus near 95-100 turn commits/s on both tmpfs and disk; beyond it the
queue grows and the 5s busy timeout fails a small share of turns. 500 idle bots
cost 62MB RSS and 1004 goroutines. The two candidate adapter settings (longer
busy timeout, bounded SQLite pool) cut failures and the 1GB RSS peak to under
100MB in isolated scratch runs. Adopting them is an owner decision: this branch
keeps them out of production code and records the evidence instead.
