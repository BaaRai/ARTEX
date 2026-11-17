package db

import "testing"

func TestActivityOutboxKeepsDetail(t *testing.T) {
	in := Activity{Worker: "worker", Kind: "tool_result", Summary: "done", Detail: "full body", IsError: true}
	raw, err := marshalActivity(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := unmarshalActivity(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.Detail != in.Detail || out.Summary != in.Summary || out.Worker != in.Worker || out.Kind != in.Kind || !out.IsError {
		t.Fatalf("round trip = %+v", out)
	}
}
