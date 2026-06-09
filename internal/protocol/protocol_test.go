package protocol

import "testing"

func TestTurnDoneJSON(t *testing.T) {
	raw, err := SerializeServerMessage(TurnDone(TurnTimings{
		STTMs: 4030, AugmentMs: 0, LLMFirstTokenMs: 856, TTSFirstByteMs: 1324, TotalMs: 7489,
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty json")
	}
	t.Log(raw)
}
