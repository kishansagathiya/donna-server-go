package pipeline

import "testing"

func TestClassifyTranscript(t *testing.T) {
	lowAudio := AudioQualityMeta{DurationMs: 500, SpeechMs: 100, PeakRms: 0.005}
	clearAttempt := AudioQualityMeta{DurationMs: 2000, SpeechMs: 800, PeakRms: 0.05}

	tests := []struct {
		name       string
		transcript string
		audio      AudioQualityMeta
		want       TranscriptClass
	}{
		{
			name:       "empty transcript short audio",
			transcript: "",
			audio:      lowAudio,
			want:       TranscriptNoise,
		},
		{
			name:       "empty transcript clear speech attempt",
			transcript: "",
			audio:      clearAttempt,
			want:       TranscriptFailedAttempt,
		},
		{
			name:       "filler only clear attempt",
			transcript: "um",
			audio:      clearAttempt,
			want:       TranscriptFailedAttempt,
		},
		{
			name:       "filler only no clear attempt",
			transcript: "uh",
			audio:      lowAudio,
			want:       TranscriptNoise,
		},
		{
			name:       "hallucination",
			transcript: "Thanks for watching!",
			audio:      clearAttempt,
			want:       TranscriptNoise,
		},
		{
			name:       "valid speech",
			transcript: "Hello Donna",
			audio:      clearAttempt,
			want:       TranscriptValid,
		},
		{
			name:       "too short meaningful chars long audio",
			transcript: "a b",
			audio:      clearAttempt,
			want:       TranscriptFailedAttempt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyTranscript(tt.transcript, tt.audio)
			if got != tt.want {
				t.Fatalf("ClassifyTranscript(%q) = %q, want %q", tt.transcript, got, tt.want)
			}
		})
	}
}
