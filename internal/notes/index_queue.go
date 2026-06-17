package notes

import (
	"context"
	"sync"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

type IndexQueue struct {
	Indexer *Indexer
	chains  map[string]chan struct{}
	mu      sync.Mutex
}

func NewIndexQueue(indexer *Indexer) *IndexQueue {
	return &IndexQueue{
		Indexer: indexer,
		chains:  make(map[string]chan struct{}),
	}
}

func (q *IndexQueue) Enqueue(noteID string) {
	if q.Indexer == nil || q.Indexer.Store == nil || !q.Indexer.Store.Enabled {
		return
	}
	q.mu.Lock()
	prev := q.chains[noteID]
	done := make(chan struct{})
	q.chains[noteID] = done
	q.mu.Unlock()

	go func() {
		if prev != nil {
			<-prev
		}
		if err := q.Indexer.IndexNote(context.Background(), noteID); err != nil {
			log.Warn("note index job failed", map[string]any{
				"noteId": log.ShortID(noteID),
				"error":  err.Error(),
			})
		}
		close(done)
		q.mu.Lock()
		if q.chains[noteID] == done {
			delete(q.chains, noteID)
		}
		q.mu.Unlock()
	}()
}
