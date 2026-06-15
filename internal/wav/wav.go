// Package wav wraps raw PCM audio in a standard RIFF/WAVE container.
// Voice clients stream PCM16; downstream STT and storage expect WAV bytes.
package wav

// PCMFormat describes the raw PCM payload before wrapping.
type PCMFormat struct {
	SampleRate int // Hz, e.g. 16000
	Channels   int // 1 = mono, 2 = stereo
}

// PCM16ToWAV prepends a 44-byte WAV header to little-endian 16-bit PCM samples.
// The output is a complete in-memory WAV file suitable for TranscribeWAV and storage.
func PCM16ToWAV(pcm []byte, format PCMFormat) []byte {
	sampleRate := format.SampleRate
	channels := format.Channels
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm)

	out := make([]byte, 44+dataSize)

	// RIFF chunk: file container
	copy(out[0:4], "RIFF")
	putUint32LE(out[4:], uint32(36+dataSize)) // chunk size = rest of file minus 8 bytes
	copy(out[8:12], "WAVE")

	// fmt sub-chunk: PCM format metadata
	copy(out[12:16], "fmt ")
	putUint32LE(out[16:], 16)                 // sub-chunk size for PCM
	putUint16LE(out[20:], 1)                  // audio format 1 = linear PCM
	putUint16LE(out[22:], uint16(channels))
	putUint32LE(out[24:], uint32(sampleRate))
	putUint32LE(out[28:], uint32(byteRate))   // bytes per second
	putUint16LE(out[32:], uint16(blockAlign)) // bytes per sample frame (all channels)
	putUint16LE(out[34:], uint16(bitsPerSample))

	// data sub-chunk: raw sample bytes from the client
	copy(out[36:40], "data")
	putUint32LE(out[40:], uint32(dataSize))
	copy(out[44:], pcm)

	return out
}

func putUint32LE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putUint16LE(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}
