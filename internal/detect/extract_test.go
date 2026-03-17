package detect

import (
	"testing"
)

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
