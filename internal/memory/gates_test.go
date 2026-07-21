package memory

import "testing"

func TestInRolloutCohort_nested(t *testing.T) {
	in5 := 0
	in25 := 0
	const n = 200
	for i := 0; i < n; i++ {
		id := "00000000-0000-4000-8000-" + padHex(i)
		if InRolloutCohort(id, RolloutPct5) {
			in5++
			if !InRolloutCohort(id, RolloutPct25) {
				t.Fatalf("user %s in five-percent cohort but not twenty-five", id)
			}
		}
		if InRolloutCohort(id, RolloutPct25) {
			in25++
		}
		if !InRolloutCohort(id, RolloutPct100) {
			t.Fatal("hundred-percent cohort should include everyone")
		}
	}
	if in5 == 0 || in5 > n/5 {
		t.Fatalf("unexpected five-percent count %d", in5)
	}
	if in25 < in5 || in25 > n/2 {
		t.Fatalf("unexpected twenty-five-percent count %d", in25)
	}
}

func TestGateConstants(t *testing.T) {
	if GateNotesCacheRenderP95Ms != 100 {
		t.Fatal(GateNotesCacheRenderP95Ms)
	}
	if GateMemoryRetrievalP95Ms != 500 {
		t.Fatal(GateMemoryRetrievalP95Ms)
	}
	if GateNotesWriteAllowsSyncLLM || GateGenericPromptAllowsEmbedding {
		t.Fatal("sync LLM / generic embed must be false")
	}
}

func padHex(i int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, 12)
	for j := 11; j >= 0; j-- {
		b[j] = hexdigits[i&15]
		i >>= 4
	}
	return string(b)
}
