package storage

import "testing"

func TestConversationFullyCompiled(t *testing.T) {
	tests := []struct {
		compiled int
		current  int
		want     bool
	}{
		{0, 3, false},
		{1, 3, false},
		{3, 3, true},
		{4, 3, true},
		{1, 0, false},
	}
	for _, tt := range tests {
		got := conversationFullyCompiled(tt.compiled, tt.current)
		if got != tt.want {
			t.Fatalf("conversationFullyCompiled(%d, %d) = %v, want %v", tt.compiled, tt.current, got, tt.want)
		}
	}
}
