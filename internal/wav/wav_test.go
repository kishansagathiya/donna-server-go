package wav

import (
	"encoding/binary"
	"testing"
)

func TestPCM16ToWAV_headerAndPayload(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}
	got := PCM16ToWAV(pcm, PCMFormat{SampleRate: 16000, Channels: 1})

	if len(got) != 44+len(pcm) {
		t.Fatalf("unexpected length: got %d want %d", len(got), 44+len(pcm))
	}
	if string(got[0:4]) != "RIFF" || string(got[8:12]) != "WAVE" {
		t.Fatalf("missing RIFF/WAVE magic: %q %q", got[0:4], got[8:12])
	}
	if string(got[36:40]) != "data" {
		t.Fatalf("missing data chunk: %q", got[36:40])
	}

	sampleRate := binary.LittleEndian.Uint32(got[24:28])
	channels := binary.LittleEndian.Uint16(got[22:24])
	if sampleRate != 16000 || channels != 1 {
		t.Fatalf("unexpected fmt metadata: rate=%d channels=%d", sampleRate, channels)
	}

	if string(got[44:]) != string(pcm) {
		t.Fatalf("PCM payload not preserved")
	}
}
