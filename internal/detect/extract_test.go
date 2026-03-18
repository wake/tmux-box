package detect

import (
	"testing"
)

func TestExtractStatusInfo(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantID    string
		wantCwd   string
		wantErr   bool
	}{
		{
			name: "full /status output",
			content: `  Session ID: 4dd75bf4-98e6-4f08-b753-08153d91c5fa
  cwd: /Users/wake/Workspace/tangency/csp-plugin
  Model:Default Opus 4.6 with 1M context
❯ `,
			wantID:  "4dd75bf4-98e6-4f08-b753-08153d91c5fa",
			wantCwd: "/Users/wake/Workspace/tangency/csp-plugin",
		},
		{
			name: "session ID without cwd",
			content: `  Session ID: deadbeef-1234-5678-9abc-def012345678
❯ `,
			wantID:  "deadbeef-1234-5678-9abc-def012345678",
			wantCwd: "",
		},
		{
			name:    "no session ID",
			content: "cwd: /tmp\n❯ ",
			wantErr: true,
		},
		{
			name: "cwd with trailing whitespace",
			content: `  Session ID: 43480073-4ab3-431d-bb07-d7e23f9b8929
  cwd: /private/tmp
  Model: something`,
			wantID:  "43480073-4ab3-431d-bb07-d7e23f9b8929",
			wantCwd: "/private/tmp",
		},
		{
			name: "bare cwd line with only whitespace yields empty",
			content: "  Session ID: deadbeef-1234-5678-9abc-def012345678\n  cwd:   \n",
			wantID:  "deadbeef-1234-5678-9abc-def012345678",
			wantCwd: "",
		},
		{
			name: "cwd with spaces in path",
			content: "  Session ID: deadbeef-1234-5678-9abc-def012345678\n  cwd: /Users/wake/My Projects/app\n",
			wantID:  "deadbeef-1234-5678-9abc-def012345678",
			wantCwd: "/Users/wake/My Projects/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ExtractStatusInfo(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.SessionID != tt.wantID {
				t.Errorf("SessionID: want %q, got %q", tt.wantID, info.SessionID)
			}
			if info.Cwd != tt.wantCwd {
				t.Errorf("Cwd: want %q, got %q", tt.wantCwd, info.Cwd)
			}
		})
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantID  string
		wantErr bool
	}{
		{
			name: "standard /status output",
			content: `  Session ID: 4dd75bf4-98e6-4f08-b753-08153d91c5fa
  cwd: /private/tmp
  Model:Default Opus 4.6 with 1M context
❯ `,
			wantID: "4dd75bf4-98e6-4f08-b753-08153d91c5fa",
		},
		{
			name:    "no session ID in content",
			content: "❯ \nsome random text\n",
			wantErr: true,
		},
		{
			name:    "empty content",
			content: "",
			wantErr: true,
		},
		{
			name: "session ID buried in noise",
			content: `lots of output here
  Session ID: deadbeef-1234-5678-9abc-def012345678
more output`,
			wantID: "deadbeef-1234-5678-9abc-def012345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ExtractSessionID(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("want %q, got %q", tt.wantID, id)
			}
		})
	}
}
