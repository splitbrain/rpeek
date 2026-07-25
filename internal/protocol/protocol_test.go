package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRequestRoundTrip checks that a Request survives a marshal/unmarshal cycle with its
// token, tool, and raw args intact.
func TestRequestRoundTrip(t *testing.T) {
	in := Request{
		Token: "secret",
		Tool:  "grep",
		Args:  json.RawMessage(`{"pattern":"error","path":"/var/log"}`),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Request
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Token != in.Token || out.Tool != in.Tool {
		t.Errorf("round trip changed fields: got %+v, want token=%q tool=%q", out, in.Token, in.Tool)
	}
	if string(out.Args) != string(in.Args) {
		t.Errorf("args = %s, want %s", out.Args, in.Args)
	}
}

// TestRequestArgsOmittedWhenEmpty checks that a Request with no args serialises without the
// "args" key and that one carrying args includes it. Tools taking no arguments send nil args.
func TestRequestArgsOmittedWhenEmpty(t *testing.T) {
	noArgs, err := json.Marshal(Request{Token: "t", Tool: "ps"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(noArgs), "args") {
		t.Errorf("empty args must be omitted, got %s", noArgs)
	}

	withArgs, err := json.Marshal(Request{Token: "t", Tool: "read", Args: json.RawMessage(`{"path":"/x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withArgs), `"args"`) {
		t.Errorf("present args must be encoded, got %s", withArgs)
	}
}

// TestResponseOmitempty checks that OK is always encoded while the optional Output,
// Truncated, and Error fields drop out when unset, keeping a successful response minimal.
func TestResponseOmitempty(t *testing.T) {
	ok, err := json.Marshal(Response{OK: true, Output: "result\n"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(ok)
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf(`missing "ok":true in %s`, got)
	}
	for _, k := range []string{"truncated", "error"} {
		if strings.Contains(got, k) {
			t.Errorf("unset %q must be omitted, got %s", k, got)
		}
	}

	// A failure keeps ok:false (no omitempty) and carries the error message.
	fail, err := json.Marshal(Response{OK: false, Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	got = string(fail)
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, `"error":"boom"`) {
		t.Errorf("failure response = %s, want ok:false and the error", got)
	}
}

// TestResponseDecode checks that a canonical server response line decodes into every field.
func TestResponseDecode(t *testing.T) {
	const line = `{"ok":true,"output":"a\nb\n","truncated":true}`
	var r Response
	if err := json.Unmarshal([]byte(line), &r); err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.Output != "a\nb\n" || !r.Truncated || r.Error != "" {
		t.Errorf("decoded = %+v", r)
	}
}
