package voicelive

import "testing"

func TestLooksLikeEcho(t *testing.T) {
	cases := []struct {
		user, asst string
		want       bool
	}{
		{
			user: "I ready when you are",
			asst: "I'm ready when you are!",
			want: true,
		},
		{
			user: "Still here What's on your mind",
			asst: "Still here! What's on your mind?",
			want: true,
		},
		{
			user: "Can you remind me about the dentist tomorrow",
			asst: "I'm ready when you are!",
			want: false,
		},
		{
			user: "ok",
			asst: "I'm ready when you are!",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := looksLikeEcho(tc.user, tc.asst); got != tc.want {
			t.Fatalf("looksLikeEcho(%q, %q) = %v, want %v", tc.user, tc.asst, got, tc.want)
		}
	}
}
