// Plain-language definitions for ARTEX's agent vocabulary, shown by <Term> as an info tooltip
// where each word first appears. Written from what the backend does (see the cited code in
// the commit that added this file); keep them short and free of other jargon.

import { t } from "@/i18n";

export const glossary = {
  goal: t(
    "One objective the task must reach. The planner splits the goal you write into one or more of these and marks each In progress, Met or Abandoned.",
  ),
  intent: t(
    "One concrete line of testing the planner proposes for a task, like “test the login form for SQL injection”. A worker takes one open intent and works it to the end.",
  ),
  fact: t(
    "Something a worker recorded while working an intent, including negative results such as “port closed”. Not a confirmed vulnerability.",
  ),
  start: t("The starting point created with every task. All intents trace back to it."),
  hint: t(
    "Advice added to a task by you, the chat agent or the orchestrator. The planner reads hints each round when deciding what to try next.",
  ),
  digest: t(
    "A summary that folds older, inactive intents and facts into one node to keep the planner's view small. The originals are kept and can be restored.",
  ),
  blackboard: t(
    "A task's saved working memory: its goals, intents, facts, hints, findings and the assets they relate to.",
  ),
  anchor: t(
    "Anchoring sets the task's scope: the domains, IPs, networks or companies it targets. Coverage is counted against this scope. It does not block anything outside it.",
  ),
  frontier: t("Open intents waiting for a free worker, highest priority first."),
  deepen: t(
    "Adds a top-priority intent to the finding's original task, so a worker pushes this vulnerability further along the path you describe.",
  ),
  tripped: t(
    "This model failed several times in a row (or hit a fatal error such as a bad key or no balance), so it is skipped for a cooldown: 1, then 5, then 30 minutes.",
  ),
  modelFallback: t(
    "An optional AI check for tool calls no rule matches. The model decides to allow, ask a person, or block. If the check fails it asks a person, and denies if nobody answers in time.",
  ),
  engineExploring: t("At least one model call is in progress."),
  engineIdle: t("Running and healthy, but no model call is in progress right now: waiting for work or between rounds."),
  engineStalled: t("An intent is marked running, but nothing has happened for over two minutes. It is probably stuck."),
  intentExhausted: t(
    "The worker hit its step or time limit. Only partial results were saved; the planner may retry this direction from a new angle.",
  ),
  intentError: t(
    "The worker's run ended in an error, usually the model or API failing after all retries. It barely ran. This is not an interception.",
  ),
};

export type GlossaryTerm = keyof typeof glossary;

/** Engine state → definition, for the "· idle" sub-label on task status badges. */
export const engineGlossary: Record<string, string> = {
  exploring: glossary.engineExploring,
  idle: glossary.engineIdle,
  stalled: glossary.engineStalled,
};
