package agent

import (
	"testing"
)

func TestPollLoopDetector(t *testing.T) {
	cases := []struct {
		name  string
		steps []struct {
			id      string
			tool    string
			input   string
			output  string
			isError bool
			want    bool
		}
	}{
		{
			name: "three_identical_webfetch_polls_interleaved_with_sleep_triggers",
			steps: []struct {
				id, tool, input, output string
				isError, want           bool
			}{
				{id: "1", tool: "WebFetch", input: `{"url":"https://a.ctf/api/v1/estate"}`, output: `{"records":173}`},
				{id: "s1", tool: "Sleep", input: `{"seconds":60}`, output: "slept"},
				{id: "2", tool: "WebFetch", input: `{"url":"https://a.ctf/api/v1/estate"}`, output: `{"records":173}`},
				{id: "s2", tool: "Sleep", input: `{"seconds":60}`, output: "slept"},
				{id: "3", tool: "WebFetch", input: `{"url":"https://a.ctf/api/v1/estate"}`, output: `{"records":173}`, want: true},
			},
		},
		{
			name: "bash_curl_get_and_bash_sleep_triggers_on_third_repeat",
			steps: []struct {
				id, tool, input, output string
				isError, want           bool
			}{
				{id: "1", tool: "Bash", input: `{"command":"curl -s https://a.ctf/status"}`, output: "churned"},
				{id: "s1", tool: "Bash", input: `{"command":"sleep 30"}`, output: ""},
				{id: "2", tool: "Bash", input: `{"command":"curl -s https://a.ctf/status"}`, output: "churned"},
				{id: "s2", tool: "Bash", input: `{"command":"sleep 30"}`, output: ""},
				{id: "3", tool: "Bash", input: `{"command":"curl -s https://a.ctf/status"}`, output: "churned", want: true},
			},
		},
		{
			name: "changing_response_body_resets_streak",
			steps: []struct {
				id, tool, input, output string
				isError, want           bool
			}{
				{id: "1", tool: "WebFetch", input: `{"url":"https://a.ctf/status"}`, output: "v1"},
				{id: "s1", tool: "Sleep", input: `{"seconds":10}`, output: "ok"},
				{id: "2", tool: "WebFetch", input: `{"url":"https://a.ctf/status"}`, output: "v2"},
				{id: "s2", tool: "Sleep", input: `{"seconds":10}`, output: "ok"},
				{id: "3", tool: "WebFetch", input: `{"url":"https://a.ctf/status"}`, output: "v2", want: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPollLoopDetector()
			for _, st := range tc.steps {
				d.onToolUse(st.id, st.tool, st.input)
				got := d.onToolResult(st.id, st.output, st.isError)
				if got != st.want {
					t.Fatalf("step %s (%s): got triggered=%v, want %v", st.id, st.tool, got, st.want)
				}
			}
		})
	}
}
