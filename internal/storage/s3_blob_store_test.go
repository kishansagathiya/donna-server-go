package storage

import "testing"

func TestNewS3BlobStoreRequiresFields(t *testing.T) {
	_, err := NewS3BlobStore(S3BlobStoreConfig{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	store, err := NewS3BlobStore(S3BlobStoreConfig{
		Bucket:          "chatgpt-imports-test",
		Endpoint:        "https://storage.railway.app",
		Region:          "auto",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !store.Enabled() || store.Bucket() != "chatgpt-imports-test" {
		t.Fatalf("unexpected store: enabled=%v bucket=%s", store.Enabled(), store.Bucket())
	}
}
