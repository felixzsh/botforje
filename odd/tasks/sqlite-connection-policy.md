# SQLite connection policy

Feature branch `fix/sqlite-connection-policy`. Evidence and measurements:
`odd/tasks/multi-bot-load-harness.md` and
`docs/channels/load-testing-plan.md` section 6.

## Specs

S1 — Bounded connection pool and a longer busy timeout in the SQLite adapter.
User authorization: "si, arranca" to the concrete plan of changing
`busy_timeout` from 5 s to 30 s and capping the pool at 8 connections, with
tests.

S2 — The canonical ADR must state the connection policy. User authorization:
"si, arranca" to "settings + tests + ADR".

S3 — The load harness must expose the production pacing knobs so a run can use
the operator's real configuration. User authorization: "si, arranca" to "y
después el pacing del harness".

S4 — The accepted per-instance target is 300 stable bots; 500 is a measured
stretch, not a promise. User request: "okey admito 300". Original expectation
for context: "un bot conteste cada mensaje que le llega aproximadamente en
digmaos a lo mucho unos 6 segundos entonces po rbot 10 mensajes por minuto".

## Tasks

| ID | Specs | Route | Commit | State |
| --- | --- | --- | --- | --- |
| T1 | S1, S2 | inline | 3dd05d3 | done |
| T2 | S3 | inline | | pending |
| T3 | S4 | inline | aed7107 | done |
| T4 | S1 | inline | 1e5a182 | done |

T1 — RED tests for the timeout and the pool bound, then the two settings and the
ADR 0006 update. Its own commit: the Risk line names a high-risk item
(concurrency).

T2 — Pacing knobs in the load harness, documented in the plan document; test-only
code, separate commit.

T3 — Measure 300 and 500 bots with pacing, repeat each point, and record the
per-message latency against the 6 s budget.

T4 — Guard the bounded pool against the starvation the verifier reproduced: a
history export holds a read snapshot for the whole HTTP stream.

## Log

L1 — User request, verbatim: "si, arranca". Authorizes the settings change with
tests and ADR, then the harness pacing.

L2 — User request, verbatim: "okey admito 300, pero entonces que hay que
implementar especificamente a que te refieres con los settings ?". Accepts 300
bots per instance as the target and asks for the concrete implementation.

L3 — Measured basis for T1, from the harness runs: at 500 bots and 83 offered
turns/s the current code failed 1333 of 2500 turns and saturated all 8 cores;
the same load with `busy_timeout=30s` and `SetMaxOpenConns(8)` completed 2500 of
2500 with zero duplicates, 85MB peak RSS, and 1.5 cores, at p95 5.6s.

L4 — Review disposition: native assess over `101e3f5..HEAD` reported
`nativeRisk: medium`, 8 paths and 187 changed lines, with `reviewDue: false`
reason `under_budget`, so no review lineage was started. The change is still
treated as high risk for concurrency: an independent read-only verifier checks
pool starvation, timeout interaction, test strength, and ADR consistency.

L5 — Independent verification of `6c27bee` reproduced a new starvation, absent in
its parent: eight stalled HTTP history exports hold all eight connections, and an
unrelated `SELECT 1` then fails its 100ms deadline. Fixed in `1e5a182` by
allowing one export at a time, acquired before the first byte, with a test that
fails at two concurrent reads. Two further findings: SQLite's busy handler
cannot observe a Go context, so a cancelled write still waits out the 30s bound
(the old 5s bound was shorter, not cancellable either); and
`TestSetupConcurrentMutationsStaySerialized` shows a pre-existing race on both
the parent and the candidate, so it is advisory and unrelated.

L6 — Paced measurements (10 messages per minute per bot, outbox capped at 20/min,
two runs each): 300 bots completed 1500/1500 and 1499/1499 with zero failures,
p50 13-16ms, p95 132-154ms, 64-67MB RSS, about 0.9 core; 500 bots completed
2500/2500 twice with zero failures but p50 3.7-7.8s and p95 8.5-14.4s, because
at that fleet size the engine sustains about 70 turns/s and the demand queues.
300 bots meet the 6s budget with margin; 500 do not.

L7 — Profile of a saturated 100-bot run and a paced 500-bot run: about 43% of CPU
in the SQLite VM and B-tree, 9% in its libc helpers, 6-8% syscalls, 8-9% runtime,
and under 0.1% in Botflota's own Go code. Postgres would move and parallelize
that SQL work, not remove it; scope sharding is the cheaper path to 1000 bots in
one process, and 300 per instance is the accepted target for now.
