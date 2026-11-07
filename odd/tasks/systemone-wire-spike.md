# System One wire spike: what a real decision endpoint answers

Measurements against `POST https://api.typesafe.ai/v1/systemone` with
`model: "jev-latest"` (Jev), to settle three numbers that the Phase 8 ADRs left
as guesses: the answer shape, the confidence field, and the budget. The API key
used was disposable and is not recorded here.

## Findings

**The wire is the documented one.** `answers` is keyed by the question id and
every answer is an object with a `type` discriminator:

```json
{"answers": {
  "intent":   {"type": "choice", "choice": "refund", "confidence": 0.5,
               "probabilities": {"refund": 0.67, "exchange": 0.33, "info": 0.0}},
  "urgent":   {"type": "noul", "noul": 0.94},
  "severity": {"type": "score", "score": 1.35, "confidence": 0.65,
               "legend": {"0": "No impact", "1": "Minor", "2": "Major", "3": "Critical"},
               "probabilities": {"0": 0.0, "1": 0.65, "2": 0.35, "3": 0.0}}
}}
```

A `noul` answer carries no `probabilities`: one number, the probability of true.
A `score` `legend` is keyed by the level index as a string. `usage` reports
`input_tokens` and `output_tokens`, so a decision's cost is observable.

**`confidence` is not `max(probabilities)`, and it is the field to gate on.**
`state` as a JSON object is accepted unchanged, which is what lets the engine
send node, graph, and recent turns as one structured value.

| Case | `choice` | `confidence` | `max(p)` | margin |
| --- | --- | --- | --- | --- |
| clear refund request | refund | 1.00 | 1.00 | 1.00 |
| "la camisa me quedó chica" | exchange | 0.97 | 0.98 | 0.96 |
| "¿me lo cambias o me devuelves el dinero?" | refund | **0.40** | **0.55** | 0.11 |
| unrelated text | info | 0.99 | 0.99 | 0.98 |
| same intent in English | refund | 1.00 | 1.00 | 1.00 |
| "ok" | info | 0.97 | 0.98 | 0.96 |

`confidence` follows the max probability on clear input and falls faster when
the distribution is flat (0.40 against a max of 0.55), which is the behaviour a
gate wants. A threshold of 0.6 accepts every case above except the near tie.

**One `choice` holds 255 options, and 255 works.** Jev's schema caps
`criteria` at 255 entries; measured at 25, 64, 100, and 255 options, each call
returned HTTP 200 in 200–390 ms with the correct option and `confidence: 1.0`.
The test disambiguated one distinctive name among same-shaped distractors, so it
proves the wire limit, not real-catalog accuracy; that belongs to the golden
conversation fixtures.

**A call carries 65 questions here, but the intersection is 64.** Jev accepts a
`questions` map with no maximum (64 and 65 questions both answered, 200). The
Workers AI route for Clef documents `maxProperties: 64`. Trading interchangeability
for 65 questions in one call is not worth it, so a decision batch is capped at 64.

**Latency leaves room for a 2 s budget.** Five routing-shaped calls (one
`choice`, four options): min 177 ms, median 229 ms, max 316 ms. The 5 s default
assumed a hosted forward pass with no evidence; 2 s is roughly six times the
observed median.

**Errors arrive in three shapes**, so the adapter must read all of them:

```json
{"detail": {"error_type": "api_usage_error", "message": "Invalid request."}}
{"detail": [{"type": "missing", "loc": ["body","questions","q","choice","criteria"],
             "msg": "Field required", "input": "{\"type\":\"choice\"}"}]}
{"detail": "Noul question must have criteria or instructions: q"}
```

`detail` is an object, an array, or a plain string. A `200` is the only success
signal; `400` and `422` both mean the request never reached a model.

## What this settles

- Gate on `answers.<id>.confidence`; keep `probabilities` for the margin and the
  tie-break (ADR 0030).
- A decision is one state plus at most 64 typed questions; a `choice` may carry
  up to 255 options (ADR 0030).
- `system_one.bounds.timeout` defaults to 2000 ms, measured, not assumed
  (ADR 0031).
- `mapping_stage_size` defaults to 255, the wire limit, so a catalog stages only
  when it is larger than one question can hold (ADR 0056).
- `confidence` is a separate calibrated field, so the threshold is a real gate
  rather than a repetition of the softmax.

## What is still unmeasured

- Accuracy on real catalogs above roughly 25 entries: the synthetic test made
  the target distinctive on purpose. Golden conversations decide whether the
  default stage size is honest.
- Whether `confidence` is calibrated the same way on another endpoint. Swapping
  to Clef is the test of the contract, and the same six cases are the check.
- Where the `noul` probability crosses from usable to noise; a `noul` is used
  for tool selection and confirmation, and both need their own fixtures.
