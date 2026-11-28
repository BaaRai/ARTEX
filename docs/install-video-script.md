# Installation video script

Audience: a first-time self-hoster who has Docker but has never seen ARTEX.
Length: 75 to 90 seconds. Format: 16:9, 1080p, terminal and browser, captions, optional calm
voiceover. The commands must match [INSTALL.md](../INSTALL.md); if the guide changes, update this
script in the same pull request.

## Before recording

- Use a clean terminal, dark theme, font size 16 or larger, and a neutral prompt such as
  `~/artex-demo`.
- Use the placeholder password `change-me-please`. Never show a real setup token, API key, cookie
  or target data. Replace the token with `SETUP-TOKEN` in post if needed.
- Pre-pull the base images so the build does not dominate the video.
- Keep the cursor movements deliberate. Cut dead time; do not speed up text the viewer must read.

## Shot list

| Time | Visual | On-screen text | Voiceover |
| --- | --- | --- | --- |
| 0:00 to 0:08 | ARTEX logo and console screenshot | Install ARTEX in three commands | ARTEX is self-hosted and installs with Docker Compose. |
| 0:08 to 0:20 | Requirement card | Docker Desktop or Docker Engine, Compose v2, buildx. An LLM key is optional at install time. | You need Docker with Compose v2 and buildx. PostgreSQL comes with the stack. |
| 0:20 to 0:38 | Terminal: clone, enter, copy env | `git clone`, `cd ARTEX`, `cp .env.example .env` | Clone the repository and create your environment file. |
| 0:38 to 0:48 | Editor open on `.env`, cursor on `POSTGRES_PASSWORD` | Set `POSTGRES_PASSWORD` | Set a database password. Add an LLM key here or later in the UI. |
| 0:48 to 1:05 | Terminal: compose up, compose ps | `docker compose up -d --build`, `docker compose ps` | Start the stack. Two containers come up, ARTEX and PostgreSQL. |
| 1:05 to 1:20 | Terminal: log grep, then browser at localhost:8787 | `docker compose logs artex \| grep "Setup token"` | The logs print a one-time setup token. Open localhost port 8787 and paste it. |
| 1:20 to 1:28 | Browser setup screen, fake token, password form | One-time setup, then choose your console password | Choose the console password and you are in. |
| 1:28 to 1:38 | Three troubleshooting cards | Cannot open: check `docker compose port artex 8787`. Docker Desktop: add the desktop overlay. Version says `dev`: run `./update.sh`. | If localhost does not answer, check the port mapping. On Docker Desktop use the overlay file. |
| 1:38 to 1:45 | End card | INSTALL.md, Discord | The full guide and troubleshooting are in INSTALL.md. Questions go to Discord. |

## Commands to record

```bash
git clone https://github.com/BaaRai/ARTEX.git
cd ARTEX
cp .env.example .env
# edit .env: POSTGRES_PASSWORD=change-me-please
docker compose up -d --build
docker compose ps
docker compose logs artex | grep "Setup token"
# open http://localhost:8787
```

Docker Desktop alternative for the troubleshooting card:

```bash
docker compose -f docker-compose.yml -f docker-compose.desktop.yml up -d --build
```

## Caption style

- Commands in monospace, one per line, no wrapping in the middle of a flag.
- Say "localhost port 8787" instead of reading the URL character by character.
- Use "one-time setup token", not "password", for the token.
- No meme captions or hype. The tone is a short, accurate setup demo.

## What not to show

- A real setup token, database password, API key or JWT.
- Real target names or captured traffic.
- A terminal history or shell prompt containing a username you do not want published.
