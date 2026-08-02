package voicelive

import (
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/memory"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

type Handler struct {
	Config    *config.Config
	Auth      auth.Config
	Retriever *memory.Retriever
	Convs     *storage.Conversations
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		log.Warn("voicelive websocket accept failed", map[string]any{"error": err.Error()})
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := r.Context()
	var (
		session  *Session
		userID   string
		rejected bool
		initOnce sync.Once
		initDone = make(chan struct{})
	)

	initSession := func() {
		initOnce.Do(func() {
			defer close(initDone)

			if h.Config.RequireAuth {
				token := r.URL.Query().Get("token")
				if token == "" {
					rejected = true
					log.Print("voicelive auth rejected", map[string]any{"code": "missing_token"})
					_ = conn.Close(4401, "missing_token")
					return
				}

				verified, err := auth.VerifyAccessToken(ctx, token, h.Auth)
				if err != nil {
					rejected = true
					log.Print("voicelive auth rejected", map[string]any{"code": "invalid_token"})
					_ = conn.Close(4401, "invalid_token")
					return
				}
				userID = verified.UserID
			}

			session = NewSession(conn, h.Config, h.Retriever, h.Convs, userID)
			log.Print("voicelive connected", map[string]any{"userId": log.ShortID(userID)})
		})
	}

	initSession()
	<-initDone
	if rejected {
		return
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if session == nil {
			continue
		}
		_ = session.HandleMessage(ctx, data)
	}

	if session != nil {
		session.End(ctx)
		log.Print("voicelive disconnected", map[string]any{
			"sessionId": session.sessionID,
			"userId":    log.ShortID(userID),
		})
	}
}
