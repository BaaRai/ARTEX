package server

import "os"

// secretEnv lists the credentials read from the environment only at startup:
// ARTEX_PG_DSN and ARTEX_SECRET_KEY by NewManager, the API keys by agent.FromEnv inside New.
// agent.FromEnv also reads ARTEX_LLM_PROXY and ARTEX_LLM_BASE_URL: both are URLs that
// can carry credentials in their userinfo (http://user:pass@proxy:3128). The ARTEX_ARCHIVE_S3_*
// keys are not read by the server, but the container may carry them and the agent's
// shells would inherit them.
var secretEnv = []string{
	"ARTEX_PG_DSN", "ARTEX_SECRET_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
	"ARTEX_LLM_PROXY", "ARTEX_LLM_BASE_URL",
	"ARTEX_ARCHIVE_S3_ACCESS_KEY_ID", "ARTEX_ARCHIVE_S3_SECRET_ACCESS_KEY", "ARTEX_ARCHIVE_S3_SESSION_TOKEN",
}

// ScrubSecretEnv removes secretEnv from the process environment once startup has
// read it. Child processes (agent shells, custom tools, MCP stdio servers) copy
// os.Environ() when they are spawned, so they no longer inherit these values.
//
// It runs inside New, after both readers and before the MCP discovery goroutine
// and the task resume loop: both can spawn children, and resuming a task starts
// workers that run shells. Scrubbing in main after New returned left that window
// open.
func ScrubSecretEnv() {
	for _, k := range secretEnv {
		_ = os.Unsetenv(k)
	}
}

// Option configures New. Options keep the test call sites unchanged: only the
// binary opts into scrubbing, because tests create several servers in one
// process and still need ARTEX_PG_DSN on later calls.
type Option func(*Server)

// WithSecretScrub makes New drop the startup secrets after reading them, before
// the MCP discovery goroutine and the task resume loop can spawn children.
func WithSecretScrub() Option { return func(s *Server) { s.scrubSecrets = true } }
