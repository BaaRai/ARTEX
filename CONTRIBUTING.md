# Contributing

## Pull requests

- **At most 7 open pull requests per contributor.** The repository enforces this with GitHub's pull request creation limit, drafts included: when you reach it, close or merge one before opening another. Review is manual, one change at a time, so a larger batch makes the queue unmanageable and slows everyone down, including you. If you have more changes queued, open one issue that lists them and wait for the current ones to be reviewed.
- **One focused change per PR.** If a change has independent parts, split it into a small number of PRs and say in each description what the other parts depend on.
- **Describe the change.** State the problem, the behavior change and what you tested. A pull request with no description is a request for changes.
- **Provider support stays generic.** Any OpenAI- or Anthropic-compatible endpoint works through a model profile; provider convenience goes in as a neutral preset (name and base URL) or a docs recipe, with no vendor logos, promotional codes, baked vendor endpoints, or vendor-specific environment variables. A new wire format needs a schema migration, a profile save/load test, and a live connection test.

## Updating Docker image pins

The Dockerfile and the PostgreSQL service in `docker-compose.yml` use a readable tag followed
by a digest. Review these pins regularly for upstream security updates. When refreshing a pin,
run `docker buildx imagetools inspect <image>:<tag>` and use the top-level index digest. Check
that the index includes `linux/amd64` and `linux/arm64` before proposing the update. Record the
old and new digests in the PR so reviewers can verify the change.

## Issues

- One problem per issue. Include the version or commit, what you ran and what you expected.

## Security

- Do not open a public issue for a vulnerability in ARTEX itself. Follow the [private reporting instructions](SECURITY.md#reporting-a-vulnerability).
