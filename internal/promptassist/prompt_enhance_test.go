package promptassist

import "testing"

func TestCleanEnhancedPrompt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips thought block and prefix",
			in:   "<think>checking intent</think>\nEnhanced prompt: Add a retry guard around the upload call and cover it with a test.",
			want: "Add a retry guard around the upload call and cover it with a test.",
		},
		{
			name: "unwraps a full prompt fence",
			in:   "```markdown\nAdd a retry guard around the upload call.\nKeep the existing API.\n```",
			want: "Add a retry guard around the upload call.\nKeep the existing API.",
		},
		{
			name: "preserves inner code fences",
			in:   "Update the handler:\n\n```go\nif err != nil { return err }\n```",
			want: "Update the handler:\n\n```go\nif err != nil { return err }\n```",
		},
		{
			name: "rejects JSON error payloads",
			in:   `{"choices":[{"message":{"content":"no"}}]}`,
			want: "",
		},
		{
			name: "rejects empty output",
			in:   "   \n\t",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cleanEnhancedPrompt(tc.in); got != tc.want {
				t.Fatalf("cleanEnhancedPrompt() = %q, want %q", got, tc.want)
			}
		})
	}
}
