#!/usr/bin/env bash
# Lightweight triage and reply bot for issues and pull requests.
# Runs inside GitHub Actions; no server involved.
#
# Triggers (see bot.yml):
#   - issue opened: exactly one triage comment per issue
#   - comment containing @artex-bot: answers that comment (works in PR
#     threads too, since issue_comment fires there)
#   - pull request opened: one first-look review comment
# The bot never posts an approving review and never replies to itself,
# to the maintainer, or to other bots (enforced in bot.yml).
set -uo pipefail

MAX_CHARS=8000
FOOTER='---
🤖 automated reply from the repo bot. A human maintainer reads everything. Ping me in any comment with `@artex-bot`.'

log() { echo "[bot] $*"; }

[ -z "${DEEPSEEK_API_KEY:-}" ] && { log "DEEPSEEK_API_KEY secret is not set; skipping."; exit 0; }

ev=$(cat "$GITHUB_EVENT_PATH")
event_name="$GITHUB_EVENT_NAME"
repo="$GITHUB_REPOSITORY"

ask_model() { # $1 = system prompt, $2 = user payload; prints the answer
  local body="${2:0:$MAX_CHARS}"
  local req
  req=$(jq -n --arg sys "$1" --arg body "$body" \
    '{model: "deepseek-chat",
      messages: [{role: "system", content: $sys}, {role: "user", content: $body}],
      max_tokens: 900, temperature: 0.2}')
  curl -sS --max-time 120 https://api.deepseek.com/chat/completions \
    -H "Authorization: Bearer $DEEPSEEK_API_KEY" \
    -H 'Content-Type: application/json' \
    -d "$req" | jq -r '.choices[0].message.content // empty'
}

repo_context() { git ls-files | head -120; }

post_comment() { # $1 = issue or PR number, $2 = markdown text
  { echo "$2"; echo; echo "$FOOTER"; } > /tmp/bot-comment.md
  if gh issue view "$1" --json pull_request --jq '.pull_request' | grep -q .; then
    gh pr comment "$1" --body-file /tmp/bot-comment.md
  else
    gh issue comment "$1" --body-file /tmp/bot-comment.md
  fi
}

eyes() { # $1 = issue or PR number
  gh api -X POST "repos/$repo/issues/$1/reactions" -f content=eyes >/dev/null 2>&1 || true
}

already_triaged() { # $1 = issue number
  gh api "repos/$repo/issues/$1/comments" --jq '.[].body' 2>/dev/null \
    | grep -q 'automated reply from the repo bot'
}

answer=""

case "$event_name" in
  issues)
    number=$(jq -r '.issue.number' <<<"$ev")
    title=$(jq -r '.issue.title' <<<"$ev")
    body=$(jq -r '.issue.body // ""' <<<"$ev")
    if already_triaged "$number"; then log "issue #$number already triaged"; exit 0; fi
    eyes "$number"
    answer=$(ask_model \
      'You are the triage bot of an open-source Go + Next.js repository (an autonomous penetration testing agent system; AGPL-3.0). Classify this report as bug, question, or feature request. If it is a question, answer it concisely using the repository file list as context. If it is a bug report, say what reproduction steps or version information are missing. If it is a feature request, restate it in one sentence. Maximum 180 words, plain markdown, reply in the language of the report, no heading.' \
      "Issue title: $title

Issue body:
$body

Repository file list:
$(repo_context)")
    [ -z "$answer" ] && { log "model returned nothing"; exit 0; }
    post_comment "$number" "$answer"
    ;;

  issue_comment)
    number=$(jq -r '.issue.number' <<<"$ev")
    body=$(jq -r '.comment.body' <<<"$ev")
    question="${body#*@artex-bot}"            # strip up to the mention
    echo "$body" | grep -qi '@artex-bot' || { log "no mention; skipping"; exit 0; }
    eyes "$number"
    answer=$(ask_model \
      'You are the helper bot of an open-source Go + Next.js repository (an autonomous penetration testing agent system; AGPL-3.0). Someone pinged you with a question about this repository. Answer it concisely and only from what the repository file list suggests; say plainly when you are not sure and suggest opening a proper issue. Maximum 180 words, plain markdown, reply in the language of the question.' \
      "Question:
$question

Repository file list:
$(repo_context)")
    [ -z "$answer" ] && { log "model returned nothing"; exit 0; }
    post_comment "$number" "$answer"
    ;;

  pull_request)
    number=$(jq -r '.pull_request.number' <<<"$ev")
    title=$(jq -r '.pull_request.title' <<<"$ev")
    body=$(jq -r '.pull_request.body // ""' <<<"$ev")
    eyes "$number"
    diff=$(gh pr diff "$number" 2>/dev/null | head -c 4000)
    answer=$(ask_model \
      'You are the first-look bot of an open-source Go + Next.js repository (an autonomous penetration testing agent system; AGPL-3.0). Summarize what this pull request changes in two or three sentences, then a short checklist: does it include or update tests, does it touch documentation where needed, does anything look inconsistent with the described goal. You are commenting for information only; you do not approve or request changes. Maximum 180 words, plain markdown.' \
      "Pull request title: $title

Pull request description:
$body

Diff (truncated):
$diff")
    [ -z "$answer" ] && { log "model returned nothing"; exit 0; }
    post_comment "$number" "$answer"
    ;;

  *)
    log "unhandled event $event_name; skipping"
    ;;
esac

log "done"
