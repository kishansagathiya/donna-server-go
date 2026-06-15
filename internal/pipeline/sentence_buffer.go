package pipeline

import "strings"

type sentenceBuffer struct {
	pending strings.Builder
}

func (b *sentenceBuffer) add(chunk string) []string {
	if chunk == "" {
		return nil
	}
	b.pending.WriteString(chunk)
	text := b.pending.String()

	var sentences []string
	for {
		end := findSentenceEnd(text)
		if end < 0 {
			break
		}
		sentence := strings.TrimSpace(text[:end])
		text = strings.TrimSpace(text[end:])
		if sentence != "" {
			sentences = append(sentences, sentence)
		}
	}

	b.pending.Reset()
	b.pending.WriteString(text)
	return sentences
}

func (b *sentenceBuffer) flush() string {
	rest := strings.TrimSpace(b.pending.String())
	b.pending.Reset()
	return rest
}

func findSentenceEnd(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '.', '!', '?':
			if i+1 >= len(s) {
				return len(s)
			}
			switch s[i+1] {
			case ' ', '\n', '\r':
				return i + 1
			}
		}
	}
	return -1
}
