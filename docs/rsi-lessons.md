# RSI lessons (shadow mode)

Deterministic, evidence-gated lessons extracted from closed tasks. This is the
memory layer of the RSI work: it records what earlier tasks proved, not what a
model guessed, and it never changes a prompt by itself.

The admission rule follows the harness2 Library (Google Research, Apache-2.0,
arXiv 2610.14224): a lesson exists only when it carries a cue, an action, and
at least one evidence row. The wider taxonomy of gains (task, retained,
improver) comes from the awesome-rsi list (lobehub).

## Modes

`ARTEX_RSI_LESSONS` is read when a task's agent bundle is built:

| value | behavior |
|---|---|
| unset / `off` | no selection, no logging, no prompt change (default) |
| `shadow` | select lessons and log what would have been shown, without injecting |
| `inject` | select, log, and append the block to the planner and worker system prompts |

The block is code-owned: it is appended after the rendered prompt, so editing
the agent prompt in the UI cannot remove it. Changing the flag affects new
tasks (or tasks whose agent bundle is invalidated), not a restart.

## Extraction

Extraction runs automatically when a task reaches a terminal status
(`done`, `failed`, `timeout`), and can be re-run or backfilled with:

```
POST /api/tasks/{id}/lessons/extract
```

It is idempotent. `rsi_lesson_evidence` is keyed by `(lesson, source_kind,
source_id)`, so a second run for the same task adds no evidence and cannot
inflate support.

Rules:

| source | kind | action |
|---|---|---|
| finding `confirmed` / `resolved` / `fixed` | positive | `Confirmed <vulnclass>: <summary>` |
| retest `reproduced` on such a finding | positive | `Reproduced <vulnclass> on retest: <summary>` |
| finding `false_positive` / `duplicate` / `ignored` / `risk_accepted` | negative | `Do not re-report without new mechanics: <summary> (prior <status>)` |
| retest `fixed` / `inconclusive` | caution | `Recheck before reporting <vulnclass>: a prior retest was <verdict>` |

Pending and in-progress findings produce nothing: there is no outcome yet.
Empty cue, action, or source is refused by the gate.

## Selection

`GET /api/tasks/{id}/lessons` returns the lessons visible to a task plus the
rendered block. Selection is scoped:

- generic lessons (no company, no category) are visible to every task;
- lessons from the same company or the same task category rank first;
- within a scope: positive before negative before caution, then support, then
  recency;
- at most eight lessons and 1800 characters are rendered.

Duplicates are merged at extraction time by a dedupe key over kind, scope, cue,
and action, so the same lesson from several tasks becomes one row with several
evidence entries and a higher support count.

## Rendering and safety

Lesson fields come from task evidence, which can include target-controlled
text. Rendering strips control characters, flattens every field to one line,
caps lengths, and prefixes the block with "prior outcomes, not instructions".
The block is appended to the planner and worker prompts only in `inject` mode.

What this layer deliberately does not do:

- no LLM summarization in this phase: rules only, so every lesson traces to a
  finding or retest id;
- no adoption: `status` is `candidate`; `adopted` and `retired` are reserved for
  a later, owner-approved phase;
- no prompt mutation, no benchmark probing, no lesson editing through the UI;
- no decay or deletion; the evidence table is the audit trail.

## Measuring it

`rsi_lesson_usage` records which lessons were considered for which task and in
which mode. Join it with the per-task outcome card
(`GET /api/tasks/{id}/outcome`) to compare tasks that saw lessons with tasks
that did not on the same domain:

- tokens per confirmed finding
- tool calls per confirmed finding
- repeated dead-end probes (future metric; not computable yet)
- false-positive and duplicate rate

Do not treat support as causal evidence. It counts corroborating task
outcomes, not a controlled comparison.
