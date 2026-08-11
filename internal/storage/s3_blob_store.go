package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ImportBlobStore is object storage for ChatGPT export ZIPs (Railway Buckets / S3).
type ImportBlobStore interface {
	Enabled() bool
	Bucket() string
	PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error)
	ObjectExists(ctx context.Context, key string) (bool, error)
	Download(ctx context.Context, key string) ([]byte, error)
}

// S3BlobStore is an S3-compatible ImportBlobStore (Railway Buckets, R2, etc.).
type S3BlobStore struct {
	client *s3.Client
	bucket string
}

// S3BlobStoreConfig configures a Railway/S3-compatible bucket client.
type S3BlobStoreConfig struct {
	Bucket          string
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// UsePathStyle forces path-style URLs (older Railway buckets may need this).
	UsePathStyle bool
}

// NewS3BlobStore builds an S3 client for ChatGPT import ZIPs.
func NewS3BlobStore(cfg S3BlobStoreConfig) (*S3BlobStore, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	endpoint := strings.TrimSpace(cfg.Endpoint)
	accessKey := strings.TrimSpace(cfg.AccessKeyID)
	secret := strings.TrimSpace(cfg.SecretAccessKey)
	if bucket == "" || endpoint == "" || accessKey == "" || secret == "" {
		return nil, fmt.Errorf("s3 blob store missing bucket/endpoint/credentials")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}

	awsCfg := aws.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			accessKey,
			secret,
			"",
		),
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(endpoint, "/"))
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &S3BlobStore{client: client, bucket: bucket}, nil
}

func (s *S3BlobStore) Enabled() bool {
	return s != nil && s.client != nil && s.bucket != ""
}

func (s *S3BlobStore) Bucket() string {
	if s == nil {
		return ""
	}
	return s.bucket
}

func (s *S3BlobStore) PresignPut(ctx context.Context, key, contentType string, expires time.Duration) (string, error) {
	if !s.Enabled() {
		return "", fmt.Errorf("s3 blob store unavailable")
	}
	if expires <= 0 {
		expires = time.Hour
	}
	key = strings.TrimPrefix(key, "/")
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	presigner := s3.NewPresignClient(s.client)
	out, err := presigner.PresignPutObject(ctx, input, s3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return out.URL, nil
}

func (s *S3BlobStore) ObjectExists(ctx context.Context, key string) (bool, error) {
	if !s.Enabled() {
		return false, fmt.Errorf("s3 blob store unavailable")
	}
	key = strings.TrimPrefix(key, "/")
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isS3NotFound(err error) bool {
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return true
	}
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "NotFound" || code == "NoSuchKey" || code == "404" {
			return true
		}
	}
	msg := err.Error()
	return strings.Contains(msg, "NotFound") ||
		strings.Contains(msg, "NoSuchKey") ||
		strings.Contains(msg, "StatusCode: 404")
}

func (s *S3BlobStore) Download(ctx context.Context, key string) ([]byte, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("s3 blob store unavailable")
	}
	key = strings.TrimPrefix(key, "/")
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}
