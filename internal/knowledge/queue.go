package knowledge

import (
	"context"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const compileDebounceMS = 8000

type Queue struct {
	KB       *storage.Knowledge
	Compiler *Compiler
	chains   map[string]chan struct{}
	mu       sync.Mutex
}

func NewQueue(kb *storage.Knowledge, compiler *Compiler) *Queue {
	return &Queue{
		KB:       kb,
		Compiler: compiler,
		chains:   make(map[string]chan struct{}),
	}
}

func (q *Queue) EnqueueSessionCompile(userID, conversationID string) {
	if q.KB == nil || !q.KB.Enabled {
		return
	}
	q.enqueue(userID, func(ctx context.Context) error {
		q.KB.LogKnowledge("compile job started", map[string]any{
			"user":           shortID(userID),
			"conversationId": conversationID,
		})
		return q.Compiler.CompileConversation(ctx, userID, conversationID)
	}, true)
}

func (q *Queue) EnqueueAssetCompile(userID, sourceID string) {
	if q.KB == nil || !q.KB.Enabled {
		return
	}
	q.enqueue(userID, func(ctx context.Context) error {
		q.KB.LogKnowledge("asset compile job started", map[string]any{
			"user":     shortID(userID),
			"sourceId": sourceID,
		})
		return q.Compiler.CompileSource(ctx, userID, sourceID)
	}, false)
}

func (q *Queue) enqueue(userID string, work func(context.Context) error, debounce bool) {
	q.mu.Lock()
	prev := q.chains[userID]
	done := make(chan struct{})
	q.chains[userID] = done
	q.mu.Unlock()

	go func() {
		if prev != nil {
			<-prev
		}
		if debounce {
			time.Sleep(compileDebounceMS * time.Millisecond)
		}
		if err := work(context.Background()); err != nil {
			log.Warn("compile job failed", map[string]any{
				"user":  shortID(userID),
				"error": err.Error(),
			})
		}
		close(done)
		q.mu.Lock()
		if q.chains[userID] == done {
			delete(q.chains, userID)
		}
		q.mu.Unlock()
	}()
}
