package wav

type PCMFormat struct {
	SampleRate int
	Channels   int
}

func PCM16ToWAV(pcm []byte, format PCMFormat) []byte {
	sampleRate := format.SampleRate
	channels := format.Channels
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm)

	out := make([]byte, 44+dataSize)
	copy(out[0:4], "RIFF")
	putUint32LE(out[4:], uint32(36+dataSize))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	putUint32LE(out[16:], 16)
	putUint16LE(out[20:], 1)
	putUint16LE(out[22:], uint16(channels))
	putUint32LE(out[24:], uint32(sampleRate))
	putUint32LE(out[28:], uint32(byteRate))
	putUint16LE(out[32:], uint16(blockAlign))
	putUint16LE(out[34:], uint16(bitsPerSample))
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
