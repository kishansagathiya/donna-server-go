package account

import (
	"context"
	"fmt"
	"net/url"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const (
	conversationAudioBucket = "conversation-audio"
	knowledgeAssetsBucket   = "knowledge-assets"
)

type Deleter struct {
	DB *storage.Supabase
}

func (d *Deleter) DeleteUser(ctx context.Context, userID string) error {
	if d.DB == nil || !d.DB.Enabled() {
		return fmt.Errorf("account deletion unavailable")
	}
	if userID == "" {
		return fmt.Errorf("missing user id")
	}

	prefix := userID + "/"
	for _, bucket := range []string{conversationAudioBucket, knowledgeAssetsBucket} {
		paths, err := d.DB.ListStorageObjects(ctx, bucket, prefix)
		if err != nil {
			return fmt.Errorf("list storage %s: %w", bucket, err)
		}
		if err := d.DB.DeleteStorageObjects(ctx, bucket, paths); err != nil {
			return fmt.Errorf("delete storage %s: %w", bucket, err)
		}
	}

	tables := []string{"kb_facts", "kb_sources", "kb_compile_log", "conversations", "kb_user_profiles"}
	for _, table := range tables {
		q := url.Values{}
		q.Set("user_id", "eq."+userID)
		if err := d.DB.Delete(ctx, table, q); err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}

	if err := d.DB.DeleteAuthUser(ctx, userID); err != nil {
		return fmt.Errorf("delete auth user: %w", err)
	}

	log.Print("account deleted", map[string]any{"userId": log.ShortID(userID)})
	return nil
}
