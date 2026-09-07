package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// payloadWithToolInput builds a cc-shaped PostToolUse payload whose
// `tool_input` string is approximately size bytes — the field the extractor
// must skip without materialising.
func payloadWithToolInput(t testing.TB, size int) json.RawMessage {
	if t != nil {
		t.Helper()
	}
	raw, err := json.Marshal(map[string]any{
		"session_id":      "sess-large",
		"cwd":             "/srv/app",
		"hook_event_name": "PostToolUse",
		"tool_name":       "Edit",
		"tool_input": map[string]any{
			"file_path":  "/srv/app/main.go",
			"new_string": strings.Repeat("x", size),
		},
	})
	if err != nil {
		panic(err)
	}
	return raw
}

func TestExtractSessionIdentity(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantSession string
		wantCwd     string
	}{
		{
			name:        "both fields",
			raw:         `{"session_id":"sess-1","cwd":"/tmp/work","hook_event_name":"Stop"}`,
			wantSession: "sess-1",
			wantCwd:     "/tmp/work",
		},
		{
			name:        "session id only",
			raw:         `{"session_id":"sess-1"}`,
			wantSession: "sess-1",
			wantCwd:     "",
		},
		{
			name:        "cwd only",
			raw:         `{"cwd":"/tmp/work"}`,
			wantSession: "",
			wantCwd:     "/tmp/work",
		},
		{
			name:        "neither",
			raw:         `{"hook_event_name":"Stop"}`,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "empty object",
			raw:         `{}`,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "null literals",
			raw:         `{"session_id":null,"cwd":null}`,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "malformed json is not an error",
			raw:         `{"session_id":"sess-1",`,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "not an object",
			raw:         `["session_id","sess-1"]`,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "empty payload",
			raw:         ``,
			wantSession: "",
			wantCwd:     "",
		},
		{
			name:        "wrongly typed session id is skipped",
			raw:         `{"session_id":42,"cwd":"/tmp/work"}`,
			wantSession: "",
			wantCwd:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotSession, gotCwd := ExtractSessionIdentity(json.RawMessage(tc.raw))
			if gotSession != tc.wantSession || gotCwd != tc.wantCwd {
				t.Fatalf("ExtractSessionIdentity(%s) = (%q, %q), want (%q, %q)",
					tc.raw, gotSession, gotCwd, tc.wantSession, tc.wantCwd)
			}
		})
	}
}

func TestExtractSessionIdentity_NilPayload(t *testing.T) {
	gotSession, gotCwd := ExtractSessionIdentity(nil)
	if gotSession != "" || gotCwd != "" {
		t.Fatalf("ExtractSessionIdentity(nil) = (%q, %q), want two empty strings", gotSession, gotCwd)
	}
}

func TestExtractSessionIdentity_LargeToolInput(t *testing.T) {
	raw := payloadWithToolInput(t, 256*1024)
	gotSession, gotCwd := ExtractSessionIdentity(raw)
	if gotSession != "sess-large" || gotCwd != "/srv/app" {
		t.Fatalf("ExtractSessionIdentity(large) = (%q, %q), want (sess-large, /srv/app)", gotSession, gotCwd)
	}
}

// BenchmarkExtractSessionIdentity backs one claim and one claim only: the
// unknown `tool_input` value is *not* materialised, so allocated bytes per op
// must not scale with its size. Run with -benchmem and compare the two
// sub-benchmarks — 64x the payload must not mean 64x the bytes. This is not a
// zero-allocation claim; encoding/json's scanner allocates.
func BenchmarkExtractSessionIdentity(b *testing.B) {
	for _, size := range []int{4 * 1024, 256 * 1024} {
		raw := payloadWithToolInput(nil, size)
		b.Run(fmt.Sprintf("tool_input_%dKiB", size/1024), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for i := 0; i < b.N; i++ {
				sessionID, cwd := ExtractSessionIdentity(raw)
				if sessionID == "" || cwd == "" {
					b.Fatalf("unexpected empty identity (%q, %q)", sessionID, cwd)
				}
			}
		})
	}
}
