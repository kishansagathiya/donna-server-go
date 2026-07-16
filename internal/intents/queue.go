package intents

import (
	"context"
	"strconv"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// Queue serializes intent extraction work per source key.
type Queue struct {
	Extractor *Extractor
	chains    map[string]chan struct{}
	mu        sync.Mutex
}

func NewQueue(extractor *Extractor) *Queue {
	return &Queue{
		Extractor: extractor,
		chains:    make(map[string]chan struct{}),
	}
}

func (q *Queue) EnqueueNote(userID, noteID, content string) {
	if q == nil || q.Extractor == nil {
		return
	}
	key := "note:" + noteID
	q.enqueue(key, SourceInput{
		UserID:     userID,
		SourceType: "note",
		SourceID:   noteID,
		Label:      "Note " + noteID,
		Content:    content,
	})
}

func (q *Queue) EnqueueConversationTurn(userID, conversationID string, turnIndex int, userTranscript string) {
	if q == nil || q.Extractor == nil {
		return
	}
	key := "turn:" + conversationID + ":" + strconv.Itoa(turnIndex)
	ti := turnIndex
	q.enqueue(key, SourceInput{
		UserID:          userID,
		SourceType:      "conversation_turn",
		SourceID:        conversationID,
		SourceTurnIndex: &ti,
		Label:           "Conversation turn " + strconv.Itoa(turnIndex),
		Content:         userTranscript,
	})
}

func (q *Queue) enqueue(key string, in SourceInput) {
	q.mu.Lock()
	prev := q.chains[key]
	done := make(chan struct{})
	q.chains[key] = done
	q.mu.Unlock()

	go func() {
		if prev != nil {
			<-prev
		}
		if err := q.Extractor.ExtractFromSource(context.Background(), in); err != nil {
			log.Warn("intent extract job failed", map[string]any{
				"key":   key,
				"user":  log.ShortID(in.UserID),
				"error": err.Error(),
			})
		}
		close(done)
		q.mu.Lock()
		if q.chains[key] == done {
			delete(q.chains, key)
		}
		q.mu.Unlock()
	}()
}
