package history

import (
	"strings"
	"testing"
)

func TestCCProjectPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"/Users/wake/Workspace/wake/tmux-box", "-Users-wake-Workspace-wake-tmux-box"},
		{"/", "-"},
		{"/tmp", "-tmp"},
	}
	for _, tt := range tests {
		got := CCProjectPath(tt.input)
		if got != tt.want {
			t.Errorf("CCProjectPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseJSONL(t *testing.T) {
	input := `{"type":"progress","data":"something"}
{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn"}}
{"type":"system","subtype":"hook"}
{"invalid json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"second"}]}}
`
	messages, err := ParseJSONL(strings.NewReader(input), 2*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(messages))
	}

	// First user message — string content should be converted to content block array
	if messages[0]["type"] != "user" {
		t.Fatalf("msg 0: want user, got %v", messages[0]["type"])
	}
	msg0 := messages[0]["message"].(map[string]interface{})
	content0 := msg0["content"].([]interface{})
	if len(content0) != 1 {
		t.Fatalf("msg 0: want 1 content block, got %d", len(content0))
	}

	// Assistant message
	if messages[1]["type"] != "assistant" {
		t.Fatalf("msg 1: want assistant, got %v", messages[1]["type"])
	}

	// Second user message — already has content block array
	if messages[2]["type"] != "user" {
		t.Fatalf("msg 2: want user, got %v", messages[2]["type"])
	}
}

func TestParseJSONLEmpty(t *testing.T) {
	messages, err := ParseJSONL(strings.NewReader(""), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if messages == nil {
		// nil is ok, but check length
		messages = []map[string]interface{}{}
	}
	if len(messages) != 0 {
		t.Fatalf("want 0, got %d", len(messages))
	}
}

func TestParseJSONLFiltersMeta(t *testing.T) {
	input := `{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat: ignore</local-command-caveat>"}}
{"type":"user","message":{"role":"user","content":"<command-name>/exit</command-name>\n            <command-message>exit</command-message>\n            <command-args></command-args>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>Catch you later!</local-command-stdout>"}}
{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"No response requested."}],"stop_reason":"stop_sequence"}}
{"type":"user","message":{"role":"user","content":"ping"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn"}}
`
	messages, err := ParseJSONL(strings.NewReader(input), 2*1024*1024)
	if err != nil {
		t.Fatal(err)
	}

	// Should get 3 messages: /exit (parsed command), ping, pong
	if len(messages) != 3 {
		t.Fatalf("want 3 messages, got %d", len(messages))
	}

	// First: /exit command (extracted from <command-name>)
	msg0 := messages[0]["message"].(map[string]interface{})
	content0 := msg0["content"].([]interface{})
	block0 := content0[0].(map[string]interface{})
	if block0["text"] != "/exit" {
		t.Errorf("want /exit, got %v", block0["text"])
	}

	// Second: ping
	msg1 := messages[1]["message"].(map[string]interface{})
	content1 := msg1["content"].([]interface{})
	block1 := content1[0].(map[string]interface{})
	if block1["text"] != "ping" {
		t.Errorf("want ping, got %v", block1["text"])
	}

	// Third: pong
	if messages[2]["type"] != "assistant" {
		t.Errorf("want assistant, got %v", messages[2]["type"])
	}
}

func TestParseJSONLSizeLimit(t *testing.T) {
	// Create input with 100 lines
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(`{"type":"user","message":{"role":"user","content":"msg"}}` + "\n")
	}
	// Very small limit — should keep tail (most recent)
	messages, err := ParseJSONL(strings.NewReader(sb.String()), 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("expected some messages despite size limit")
	}
	if len(messages) >= 100 {
		t.Fatal("expected truncation")
	}
}
