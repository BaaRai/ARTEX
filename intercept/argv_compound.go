package intercept

import (
	"encoding/json"
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// argvMaxNestedPayloads bounds how deeply nested shell/eval payloads
// (for example, bash -c "bash -c ...") are re-parsed and walked.
const argvMaxNestedPayloads = 3

// callWord is one argument word of a *syntax.CallExpr for deny/ask matching.
// Literal is true when the word contains no runtime expansion; Value holds the
// unquoted text. AssignLike is true when the word begins with a literal
// NAME= prefix (for example FOO=1 or FOO=$(id)), so wrappers like env and sudo
// can skip environment assignments without skipping arbitrary opaque tokens.
type callWord struct {
	Value      string
	Literal    bool
	AssignLike bool
}

// argvCompoundMatches reports whether any command executed within a Bash tool
// call matches the deny/ask prefix rule tokens. It parses the entire Bash line,
// walks every *syntax.CallExpr in the AST, strips known wrapper programs, and
// recurses into eval and shell -c payloads up to argvMaxNestedPayloads deep.
//
// Parse errors never panic and always return false (no match).
func argvCompoundMatches(tokens []string, toolName string, input []byte) bool {
	if toolName != "Bash" || len(tokens) == 0 {
		return false
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(input, &in) != nil {
		return false
	}
	if strings.TrimSpace(in.Command) == "" {
		return false
	}
	return bashCommandMatchesArgv(tokens, in.Command, 0)
}

func bashCommandMatchesArgv(rule []string, command string, depth int) bool {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	matched := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if matched {
			return false
		}
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]callWord, len(call.Args))
		for i, w := range call.Args {
			words[i] = callWordOf(w)
		}
		if callMatchesArgv(rule, words, depth) {
			matched = true
			return false
		}
		return true
	})
	return matched
}

// callMatchesArgv checks a single call's argv against rule, stripping known
// wrappers repeatedly and recursing into eval / shell -c payloads up to
// argvMaxNestedPayloads deep.
func callMatchesArgv(rule []string, argv []callWord, depth int) bool {
	cur := argv
	for len(cur) > 0 {
		if argvMatchesRulePrefix(rule, cur) {
			return true
		}
		if !cur[0].Literal || cur[0].Value == "" {
			return false
		}
		prog := path.Base(cur[0].Value)

		// Nested Bash payload wrappers: eval and shells with -c.
		if prog == "eval" {
			if depth >= argvMaxNestedPayloads {
				return false
			}
			payload, ok := evalPayload(cur[1:])
			if !ok {
				return false
			}
			return bashCommandMatchesArgv(rule, payload, depth+1)
		}
		if isShellWrapper(prog) {
			if depth >= argvMaxNestedPayloads {
				return false
			}
			payload, ok := shellDashCPayload(cur[1:])
			if !ok {
				return false
			}
			return bashCommandMatchesArgv(rule, payload, depth+1)
		}

		// Direct command-wrapping programs.
		next, ok := stripOneWrapper(prog, cur[1:])
		if !ok || len(next) == 0 || len(next) >= len(cur) {
			return false
		}
		cur = next
	}
	return false
}

// argvMatchesRulePrefix compares rule against the first len(rule) tokens of
// argv. For token 0, a rule token without a path separator matches the basename
// of the actual program; a rule token with a separator must match verbatim.
func argvMatchesRulePrefix(rule []string, argv []callWord) bool {
	if len(rule) == 0 || len(argv) < len(rule) {
		return false
	}
	for i, want := range rule {
		if !argv[i].Literal {
			return false
		}
		got := argv[i].Value
		if i == 0 {
			if got == "" {
				return false
			}
			if !strings.ContainsAny(want, "/\\") {
				got = path.Base(got)
			}
		}
		if got != want {
			return false
		}
	}
	return true
}

// callWordOf converts a Bash AST word into a callWord. Words containing any
// expansion (parameter, command/process substitution, arithmetic, glob, brace,
// tilde, ANSI-C/locale quoting) become opaque (Literal: false) rather than
// failing the whole call.
func callWordOf(w *syntax.Word) callWord {
	if w == nil || len(w.Parts) == 0 {
		return callWord{}
	}
	assignLike := false
	if firstLit, ok := w.Parts[0].(*syntax.Lit); ok && hasAssignPrefix(firstLit.Value) {
		assignLike = true
	}

	var sb strings.Builder
	literal := true
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			s, ok := unescapeBareLit(p.Value)
			if !ok {
				literal = false
				continue
			}
			sb.WriteString(s)
		case *syntax.SglQuoted:
			if p.Dollar {
				literal = false
				continue
			}
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				literal = false
				continue
			}
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					literal = false
					break
				}
				sb.WriteString(unescapeDblQuotedLit(lit.Value))
			}
		default:
			literal = false
		}
	}
	if !literal {
		return callWord{Literal: false, AssignLike: assignLike}
	}
	val := sb.String()
	if !assignLike && hasAssignPrefix(val) {
		assignLike = true
	}
	return callWord{Value: val, Literal: true, AssignLike: assignLike}
}

func unescapeBareLit(s string) (string, bool) {
	var sb strings.Builder
	sb.Grow(len(s))
	hasOpenBrace := false
	hasCloseBrace := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return "", false
			}
			i++
			if s[i] == '\n' {
				continue
			}
			sb.WriteByte(s[i])
			continue
		}
		switch c {
		case '*', '?', '[', ']', '~':
			return "", false
		case '{':
			hasOpenBrace = true
		case '}':
			hasCloseBrace = true
		}
		sb.WriteByte(c)
	}
	if hasOpenBrace && hasCloseBrace && (strings.Contains(s, ",") || strings.Contains(s, "..")) {
		return "", false
	}
	return sb.String(), true
}

func unescapeDblQuotedLit(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\n':
				i++
				continue
			case '"', '$', '`', '\\':
				i++
				sb.WriteByte(s[i])
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func hasAssignPrefix(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := s[i]
		isLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

func isShellWrapper(prog string) bool {
	switch prog {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		return true
	}
	return false
}

// shellDashCPayload extracts the literal command string following a leading
// "-c" option. Other shell options are intentionally not interpreted.
func shellDashCPayload(args []callWord) (string, bool) {
	if len(args) < 2 {
		return "", false
	}
	if !args[0].Literal || args[0].Value != "-c" {
		return "", false
	}
	if !args[1].Literal {
		return "", false
	}
	return args[1].Value, true
}

// evalPayload joins eval's arguments with spaces for re-parsing as Bash.
// A leading "--" is stripped. Opaque arguments are substituted with an
// unexpanded placeholder ($__opaque) so they never shift a later literal word
// into command position, while trailing opaque args still let a leading literal
// command match.
func evalPayload(args []callWord) (string, bool) {
	if len(args) > 0 && args[0].Literal && args[0].Value == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(args))
	hasLiteral := false
	for _, a := range args {
		if a.Literal {
			parts = append(parts, a.Value)
			hasLiteral = true
		} else {
			parts = append(parts, "$__opaque")
		}
	}
	if !hasLiteral {
		return "", false
	}
	return strings.Join(parts, " "), true
}

// wrapperSpec describes the handled leading-option layout for a wrapper
// program. Any flag not listed in shortNoArg, shortArg, longNoArg, longArg,
// or longOptVal causes stripping to skip the call rather than guess.
type wrapperSpec struct {
	shortNoArg   string          // short flags that take no argument
	shortArg     string          // short flags that take one argument (attached or next token)
	shortOptAtt  string          // short flags that accept only an optional attached argument (never next token)
	longNoArg    map[string]bool // --flag (no value)
	longArg      map[string]bool // --flag VAL or --flag=VAL
	longOptVal   map[string]bool // --flag or --flag=VAL (never consumes next token)
	allowBareMin bool            // treat bare "-" as a no-arg option (env -)
	allowNumFlag bool            // treat -<digits> as a no-arg option (nice -10)
	skipAssigns  bool            // skip NAME=VALUE tokens after options (env, sudo)
	posBeforeCmd int             // required positional arguments before the wrapped command
}

func strSet(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var wrapperSpecs = map[string]wrapperSpec{
	"env": {
		shortNoArg:   "i0v",
		shortArg:     "uCSa",
		longNoArg:    strSet("ignore-environment", "null", "debug"),
		longArg:      strSet("unset", "chdir", "split-string", "argv0"),
		longOptVal:   strSet("block-signal", "default-signal", "ignore-signal"),
		allowBareMin: true,
		skipAssigns:  true,
	},
	"command": {
		shortNoArg: "p",
	},
	"builtin": {},
	"exec": {
		shortNoArg: "cl",
		shortArg:   "a",
	},
	"nohup": {},
	"setsid": {
		shortNoArg: "cfw",
		longNoArg:  strSet("ctty", "fork", "wait"),
	},
	"stdbuf": {
		shortArg: "ioe",
		longArg:  strSet("input", "output", "error"),
	},
	"nice": {
		shortArg:     "n",
		longArg:      strSet("adjustment"),
		allowNumFlag: true,
	},
	"ionice": {
		shortNoArg: "t",
		shortArg:   "cn",
		longNoArg:  strSet("ignore"),
		longArg:    strSet("class", "classdata"),
	},
	"timeout": {
		shortNoArg:   "fpv",
		shortArg:     "ks",
		longNoArg:    strSet("foreground", "preserve-status", "verbose"),
		longArg:      strSet("kill-after", "signal"),
		posBeforeCmd: 1, // DURATION
	},
	"taskset": {
		shortNoArg:   "ca",
		longNoArg:    strSet("cpu-list", "all-tasks"),
		posBeforeCmd: 1, // MASK / CPU-LIST
	},
	"chrt": {
		shortNoArg:   "abdforiRv",
		shortArg:     "TPD",
		longNoArg:    strSet("all-tasks", "batch", "deadline", "fifo", "idle", "other", "rr", "reset-on-fork", "verbose"),
		longArg:      strSet("sched-runtime", "sched-period", "sched-deadline"),
		posBeforeCmd: 1, // PRIORITY
	},
	"sudo": {
		shortNoArg:  "AbBEHikKnPsSv",
		shortArg:    "CDgprRtTuU",
		longNoArg:   strSet("askpass", "background", "bell", "set-home", "login", "reset-timestamp", "remove-timestamp", "non-interactive", "preserve-groups", "shell", "stdin", "validate"),
		longArg:     strSet("close-from", "chdir", "group", "host", "prompt", "role", "chroot", "type", "command-timeout", "user", "other-user"),
		longOptVal:  strSet("preserve-env"),
		skipAssigns: true,
	},
	"doas": {
		shortNoArg: "Lns",
		shortArg:   "au",
	},
	"watch": {
		shortNoArg: "bcCdegprtwx",
		shortArg:   "nq",
		longNoArg:  strSet("beep", "color", "no-color", "errexit", "chgexit", "precise", "no-rerun", "no-title", "no-wrap", "exec"),
		longArg:    strSet("interval", "equexit"),
		longOptVal: strSet("differences"),
	},
	"flock": {
		shortNoArg:   "sxeunFo",
		shortArg:     "wE",
		longNoArg:    strSet("shared", "exclusive", "unlock", "nb", "nonblock", "no-fork", "close", "verbose"),
		longArg:      strSet("timeout", "wait", "conflict-exit-code"),
		posBeforeCmd: 1, // LOCKFILE
	},
	"strace": {
		shortNoArg: "cCdfFikqrtTvwxyYzZ",
		shortArg:   "abeEIoOpPsSuUX",
		longNoArg:  strSet("summary-only", "summary", "debug", "follow-forks", "instruction-pointer", "stack-traces", "quiet", "relative-timestamps", "absolute-timestamps", "syscall-times", "no-abbrev", "summary-wall-clock", "hex", "decode-fds", "decode-pids", "successful-only", "failed-only"),
		longArg:    strSet("columns", "detach-on", "trace", "env", "interruptible", "output", "output-separately", "attach", "trace-path", "string-limit", "summary-sort-by", "user", "signal", "status", "inject", "fault", "raw", "read", "write", "abbrev", "verbose", "silence", "strings-in-hex", "const-print-style"),
	},
	"ltrace": {
		shortNoArg: "bcCfiLrStT",
		shortArg:   "aADeFlnopsuwx",
		longNoArg:  strSet("no-signals", "demangle", "syscalls"),
		longArg:    strSet("align", "arraylen", "config", "debug", "library", "indent", "output", "attach", "strsize", "username", "where"),
	},
	"xargs": {
		shortNoArg:  "0oprtx",
		shortArg:    "adEILnPsR",
		shortOptAtt: "ei",
		longNoArg:   strSet("null", "open-tty", "interactive", "no-run-if-empty", "verbose", "exit", "show-limits"),
		longArg:     strSet("arg-file", "delimiter", "eof", "replace", "max-lines", "max-args", "max-procs", "max-chars", "process-slot-var"),
	},
}

func stripOneWrapper(prog string, args []callWord) ([]callWord, bool) {
	spec, ok := wrapperSpecs[prog]
	if !ok {
		return nil, false
	}
	i := 0
	for i < len(args) {
		if !args[i].Literal {
			if spec.skipAssigns && args[i].AssignLike {
				break
			}
			// Do not guess past an opaque token in option position.
			return nil, false
		}
		tok := args[i].Value
		if tok == "--" {
			i++
			break
		}
		if tok == "-" {
			if spec.allowBareMin {
				i++
				continue
			}
			break
		}
		if !strings.HasPrefix(tok, "-") {
			break
		}
		if strings.HasPrefix(tok, "--") {
			name := tok[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				flagName := name[:eq]
				if spec.longArg[flagName] || spec.longOptVal[flagName] {
					i++
					continue
				}
				return nil, false
			}
			if spec.longNoArg[name] || spec.longOptVal[name] {
				i++
				continue
			}
			if spec.longArg[name] {
				if i+1 >= len(args) {
					return nil, false
				}
				i += 2
				continue
			}
			return nil, false
		}

		// Short flag or combined short-flag cluster (e.g. -nu root, -uFOO).
		flags := tok[1:]
		if spec.allowNumFlag && isAllDigits(flags) {
			i++
			continue
		}
		consumedNext := false
		for j := 0; j < len(flags); j++ {
			ch := flags[j]
			switch {
			case strings.IndexByte(spec.shortNoArg, ch) >= 0:
				continue
			case strings.IndexByte(spec.shortOptAtt, ch) >= 0:
				// Optional value only when attached in the same token; never consumes next token.
				j = len(flags)
			case strings.IndexByte(spec.shortArg, ch) >= 0:
				if j+1 < len(flags) {
					// Attached value consumes the rest of this token.
					j = len(flags)
				} else {
					if i+1 >= len(args) {
						return nil, false
					}
					consumedNext = true
				}
			default:
				// Unhandled flag: skip this call rather than guessing whether it takes a value.
				return nil, false
			}
		}
		if consumedNext {
			i += 2
		} else {
			i++
		}
	}

	if spec.skipAssigns {
		for i < len(args) && args[i].AssignLike {
			i++
		}
	}

	if spec.posBeforeCmd > 0 {
		if i+spec.posBeforeCmd > len(args) {
			return nil, false
		}
		i += spec.posBeforeCmd
	}

	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
