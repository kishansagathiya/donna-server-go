package providers

import "testing"

func TestPrepareTextForSpeech(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips https url",
			input: "Check https://example.com/docs for details.",
			want:  "Check for details.",
		},
		{
			name:  "strips www url",
			input: "Visit www.example.com today.",
			want:  "Visit today.",
		},
		{
			name:  "keeps markdown link label",
			input: "Open [the guide](https://example.com/guide) when ready.",
			want:  "Open the guide when ready.",
		},
		{
			name:  "speaks slash as or for alternatives",
			input: "Would you like tea/coffee?",
			want:  "Would you like tea or coffee?",
		},
		{
			name:  "handles chained alternatives",
			input: "Pick small/medium/large.",
			want:  "Pick small or medium or large.",
		},
		{
			name:  "handles and or slash",
			input: "Add and/or remove items.",
			want:  "Add and or remove items.",
		},
		{
			name:  "strips bare domain path",
			input: "See example.com/path for more.",
			want:  "See for more.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrepareTextForSpeech(tc.input); got != tc.want {
				t.Fatalf("PrepareTextForSpeech(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
