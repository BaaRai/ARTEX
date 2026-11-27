package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// ErrPollLoopDetected is returned when a worker repeats identical read-only
// polls interleaved with sleep/Sleep instead of escalating via ask_operator.
var ErrPollLoopDetected = errors.New("polling loop detected: repeated identical read-only polls with sleep; stop polling and use ask_operator to request a fresh instance")

const pollLoopRepeatThreshold = 3

var (
	bashSleepRe      = regexp.MustCompile(`(?i)(?:^|[;&|\s])sleep\s+[0-9]+`)
	bashOnlySleepRe  = regexp.MustCompile(`(?i)^\s*(?:sleep\s+[0-9]+(?:\s*[;&|]+\s*)?)+\s*$`)
	bashMutateHintRe = regexp.MustCompile(`(?i)(?:^|\s)(?:-X|--request)\s*=*\s*["']?(?:POST|PUT|PATCH|DELETE)\b|(?:-d|--data|--data-raw|--data-binary|-F|--form)(?:\s|=)`)
	untrustedFenceRe = regexp.MustCompile(`(?m)^<<<(?:END_)?UNTRUSTED_OUTPUT [^\r\n]+>>>>\r?\n?`)
)

type pollLoopDetector struct {
	pending      map[string]string // toolUseID -> canonical read-only poll signature
	lastSig      string
	lastRespHash string
	sawSleep     bool
	repeats      int
}

func newPollLoopDetector() *pollLoopDetector {
	return &pollLoopDetector{pending: map[string]string{}}
}

func (d *pollLoopDetector) onToolUse(toolUseID, toolName, inputJSON string) {
	if d == nil {
		return
	}
	if toolName == "Sleep" {
		d.sawSleep = true
		return
	}
	if toolName == "Bash" {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal([]byte(inputJSON), &in)
		cmd := strings.TrimSpace(in.Command)
		if bashSleepRe.MatchString(cmd) {
			d.sawSleep = true
			if bashOnlySleepRe.MatchString(cmd) {
				return
			}
		}
		if cmd == "" || bashMutateHintRe.MatchString(cmd) {
			d.reset()
			return
		}
		d.pending[toolUseID] = "Bash:" + strings.Join(strings.Fields(cmd), " ")
		return
	}
	if toolName == "WebFetch" {
		var in struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(inputJSON), &in)
		m := strings.ToUpper(strings.TrimSpace(in.Method))
		if m != "" && m != "GET" && m != "HEAD" {
			d.reset()
			return
		}
		d.pending[toolUseID] = "WebFetch:" + strings.TrimSpace(in.URL)
		return
	}
	// Other tools (e.g. ask_operator, record_fact) reset polling streak unless pure read.
	d.reset()
}

func (d *pollLoopDetector) onToolResult(toolUseID, output string, isError bool) bool {
	if d == nil {
		return false
	}
	sig, ok := d.pending[toolUseID]
	delete(d.pending, toolUseID)
	if !ok || isError {
		return false
	}
	normOut := strings.TrimSpace(untrustedFenceRe.ReplaceAllString(output, ""))
	h := sha256.Sum256([]byte(normOut))
	respHash := hex.EncodeToString(h[:])
	if sig == d.lastSig && respHash == d.lastRespHash {
		if d.sawSleep {
			d.repeats++
			d.sawSleep = false
			if d.repeats >= pollLoopRepeatThreshold {
				return true
			}
		}
		return false
	}
	d.lastSig = sig
	d.lastRespHash = respHash
	d.repeats = 1
	d.sawSleep = false
	return false
}

func (d *pollLoopDetector) reset() {
	d.lastSig = ""
	d.lastRespHash = ""
	d.repeats = 0
	d.sawSleep = false
}
