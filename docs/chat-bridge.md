# Chat Bot (Inbound Chat Bridge)

The chat bot lets an operator talk to ARTEX from a chat app instead of the web UI. Telegram is the
first transport. This page covers connecting a bot, who may use it, the task commands and the
main-agent conversation. Remote approvals build on it
([issue #5](https://github.com/BaaRai/ARTEX/issues/5)).

Outbound notifications (pushing findings to a chat) are a separate feature under
**System → Notifications** and keep working without the bot.

---

## Setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token. Give ARTEX a bot of
   its own: Telegram allows one consumer per token, so a token already used by another poller or by
   a webhook fails with a conflict error.
2. Open **System → Chat bot**, paste the token, switch the bot on and save. **Test connection** asks
   Telegram which bot the token belongs to; it sends no message. The status card shows **Online**
   and the bot's username once it is polling.
3. With the allowlist still empty the bot serves no chat. From each chat you want to use (a private
   chat or a group), send the bot any message: it answers once with a refusal that names the chat ID.
4. Add those chat IDs to **Allowed chat IDs** and save. The change applies at once.
5. For a group, also add the user ID of each person who may use the bot there to **Allowed user
   IDs**. A member who is not listed gets a refusal naming their user ID.
6. Send `/help` to see the commands. `/whoami` shows the chat ID and your user ID.
7. Send `/tasks`, then `/task <id>` to pick the task this chat works on.

## Commands

| Command | What it does |
| --- | --- |
| `/tasks` | The ten most recent tasks (pinned first) with status and finding counts |
| `/task <id>` | Select the task this chat works on. Without an id, show the selected task |
| `/pause [id]` | Pause the task, as the pause button in the web UI does |
| `/resume [id]` | Resume the task. It may be queued if the concurrency limit is reached |
| `/findings [id]` | Finding counts and the ten most severe findings: title, severity and status |
| `/whoami` | The chat ID and your user ID |
| `/help` | The command list |

`[id]` defaults to the selected task. Each chat keeps its own selection, stored in the database, so
it survives a restart and two chats can work on different tasks.

Any message that is not a command goes to the **main agent** of the selected task. It is the same
conversation as the task's web chat: the message shows up there, the agent works as it would for a
web message, and its answer is sent back to the chat when the turn ends. While the agent is still
answering, a new message is refused; send it again after the answer arrives. Pausing the task stops
the turn in progress, and the chat is told so.

Replies link back to ARTEX when **Link-back URL** is set under **System → Notifications**.

## Security model

- **Only allowlisted chats are served.** A chat that is not listed gets one refusal line, naming its
  chat ID, and is then ignored. The bot remembers up to 1024 refused chats, across saves of the same
  bot; past that it starts over, so a chat may be refused one more time but a new chat always learns
  its ID. An empty allowlist serves no chat at all; that is the state a new bot starts in while you
  collect chat IDs.
- **In a group, only allowed users are served.** A group is a shared space, so allowing it is not
  enough: the sender must also be in **Allowed user IDs**, and a group with no allowed users serves
  nobody. Someone added to the group later gains nothing. A member who is not listed gets one
  refusal naming their user ID. A private chat is its own user and needs no entry.
- **Summaries and links only.** Replies carry task and finding titles, statuses and counts, never
  finding evidence or summaries. The main agent's answer is relayed with code blocks removed (that is
  where agents put requests, responses and tool output), the task's stored credentials and common
  secret shapes (authorization and cookie headers, bearer tokens, JWTs, keys, `password=…`)
  masked, and the text cut to 1500 characters; the full answer stays in ARTEX. An answer is dropped
  if its chat was removed from the allowlist while the agent worked.
- **No inbound port.** The bot uses long polling (`getUpdates`) over outbound HTTPS.
- **The token is a secret.** It is encrypted at rest with `keys/secret.key` like other stored
  secrets, shown masked in the UI and API, and never written to logs or error messages (errors keep
  only the scheme and host of the API address). Changing the API address requires entering the
  token again, so a stored token is never sent to a new host. The API address must use `https`:
  the token travels in the URL path, and the replies are the updates the bot acts on. Plain `http`
  is accepted only with `ARTEX_NOTIFY_ALLOW_LOCAL=1`, for a local test server.
- **Same network guard as notifications.** Calls refuse loopback and link-local addresses (including
  cloud metadata) unless `ARTEX_NOTIFY_ALLOW_LOCAL=1`, and never follow a cross-host redirect.
- **No replays.** The update cursor is committed before a batch is acted on, so a crash loses a
  batch rather than running it twice. Messages older than two minutes when they reach ARTEX (for
  example a backlog queued while ARTEX was down) are dropped and logged instead of executed.
- **Message text is not logged.** Logs (tag `[tgbot]`) record the chat, the sender and the decision,
  plus which user paused, resumed or messaged which task.

## Configuration changes

Saving the form takes effect immediately, without a restart: a poll in progress is interrupted and
the new configuration is read. A new token clears the update cursor, because Telegram update IDs
are per bot. A batch fetched under a configuration that was replaced during the poll is dropped.

## API

All routes require the administrator JWT.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/chat-bridges` | Every transport with its masked configuration, allowlist and live status |
| `PUT` | `/api/chat-bridges/{kind}` | Save `enabled`, `config`, `allowed_chats` and `allowed_users`; omitted fields keep their stored value |
| `POST` | `/api/chat-bridges/{kind}/test` | Check the stored token and return the bot identity |

`config` follows the notification channel convention: a masked value sent back unchanged keeps the
stored secret, and an empty string clears a field. Telegram's fields are `bot_token` and the
optional `base_url` (an `https` address of a self-hosted Bot API server). The live `status.state`
is `off`, `connecting`, `online` or `error`, with `last_error` when it fails.
