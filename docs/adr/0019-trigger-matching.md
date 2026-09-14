# ADR 0019 — Trigger matching

Status: Accepted (2026-09)

## Context

Edges carry an `intent` and optional `triggers`. Deterministic routing must
decide, per user text and candidate node, whether a trigger matches and how
strongly, so the best edge wins and ties go to the most recent node (ADR
0010). The plan left the algorithm and defaults open for Phase 4.

## Decision

Both sides are normalized first: lowercase, letters and digits kept
(Unicode-aware), everything else becomes a word boundary, and runs of
whitespace collapse. An edge scores its best trigger:

- `1.0` the normalized strings are equal.
- `0.9` the trigger appears as contiguous words.
- `0.7` every trigger word appears anywhere in the text.
- The similarity `1 - levenshtein / longest` when it is at least `0.8`, which
  tolerates typos.
- `0` otherwise.

An edge matches when its best score is at least `0.7`. Among matching edges
the highest score wins; ties go to the later candidate node (ADR 0010). Digits
and short triggers are exact matches, which keeps menu options predictable.

## Consequences

- Menu numbers and single words match exactly; paraphrases match by words;
  typos within about a fifth of the text still match.
- The threshold and the discrete scores live in one place and are pinned by
  tests.
- No stemmer, synonyms, or embeddings: the LLM path (Phase 8) covers what
  matching misses.
- Ties prefer recent nodes, so "back" works without duplicating edges.
