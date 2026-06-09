package pipeline

import (
	"encoding/binary"
	"math"
)

const (
	minAudioMS         = 300
	minSpeechRMS       = 0.01
	speechRMSThreshold = 0.01
	frameSamples       = 320
)

type AudioQualityMeta struct {
	DurationMs float64
	SpeechMs   float64
	AvgRms     float64
	PeakRms    float64
}

type AudioQualityVerdict struct {
	AudioQualityMeta
	ShouldProcess bool
	Reason        string
}

func AnalyzePCM16(pcm []byte, sampleRate, channels int) AudioQualityVerdict {
	sampleCount := len(pcm) / 2 / channels
	durationMs := float64(sampleCount) / float64(sampleRate) * 1000

	if durationMs < minAudioMS {
		return AudioQualityVerdict{
			AudioQualityMeta: AudioQualityMeta{DurationMs: durationMs},
			ShouldProcess:    false,
			Reason:           "audio_too_short",
		}
	}

	mono := pcm16ToFloat(pcm)

	var speechMs float64
	var rmsSum float64
	var peakRms float64
	frameCount := 0
	frameMs := float64(frameSamples) / float64(sampleRate) * 1000

	for offset := 0; offset < len(mono); offset += frameSamples {
		end := offset + frameSamples
		if end > len(mono) {
			end = len(mono)
		}
		frame := mono[offset:end]
		if len(frame) == 0 {
			continue
		}

		rms := computeRMS(frame)
		rmsSum += rms
		frameCount++
		if rms > peakRms {
			peakRms = rms
		}
		if rms >= speechRMSThreshold {
			speechMs += frameMs
		}
	}

	avgRms := 0.0
	if frameCount > 0 {
		avgRms = rmsSum / float64(frameCount)
	}

	meta := AudioQualityMeta{
		DurationMs: durationMs,
		SpeechMs:   speechMs,
		AvgRms:     avgRms,
		PeakRms:    peakRms,
	}

	if peakRms < minSpeechRMS {
		return AudioQualityVerdict{
			AudioQualityMeta: meta,
			ShouldProcess:    false,
			Reason:           "low_energy",
		}
	}

	return AudioQualityVerdict{
		AudioQualityMeta: meta,
		ShouldProcess:    true,
	}
}

func pcm16ToFloat(pcm []byte) []float32 {
	count := len(pcm) / 2
	out := make([]float32, count)
	for i := 0; i < count; i++ {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2:]))
		if sample < 0 {
			out[i] = float32(sample) / 0x8000
		} else {
			out[i] = float32(sample) / 0x7fff
		}
	}
	return out
}

func computeRMS(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}
