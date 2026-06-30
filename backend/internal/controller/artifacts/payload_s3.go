package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3ObjectClient interface {
	Put(context.Context, string, string, []byte) error
	Get(context.Context, string, string) ([]byte, error)
	Delete(context.Context, string, string) error
}

type s3PayloadStore struct {
	client   s3ObjectClient
	bucket   string
	prefix   string
	rootPath string
}

func newS3PayloadStore(cfg Config) (*s3PayloadStore, error) {
	client, err := newMinioObjectClient(cfg)
	if err != nil {
		return nil, err
	}
	return newS3PayloadStoreWithClient(cfg, client)
}

func newS3PayloadStoreWithClient(cfg Config, client s3ObjectClient) (*s3PayloadStore, error) {
	if client == nil {
		return nil, fmt.Errorf("artifact s3 client is nil")
	}
	bucket := strings.TrimSpace(cfg.PayloadS3Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("artifact payload s3 bucket is empty")
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.PayloadS3Prefix), "/")
	rootPath := "s3://" + bucket
	if prefix != "" {
		rootPath += "/" + prefix
	}
	return &s3PayloadStore{
		client:   client,
		bucket:   bucket,
		prefix:   prefix,
		rootPath: rootPath,
	}, nil
}

func (s *s3PayloadStore) Backend() string        { return "s3" }
func (s *s3PayloadStore) RootPath() string       { return s.rootPath }
func (s *s3PayloadStore) Container() string      { return s.bucket }
func (s *s3PayloadStore) SharedSurvivable() bool { return true }

func (s *s3PayloadStore) Write(ctx context.Context, key string, payload []byte) (PayloadWriteResult, error) {
	objectKey := s.objectKey(key)
	if err := s.client.Put(ctx, s.bucket, objectKey, payload); err != nil {
		return PayloadWriteResult{}, err
	}
	sum := sha256.Sum256(payload)
	return PayloadWriteResult{
		SizeBytes: int64(len(payload)),
		Checksum:  hex.EncodeToString(sum[:]),
	}, nil
}

func (s *s3PayloadStore) Read(ctx context.Context, key string) ([]byte, error) {
	return s.client.Get(ctx, s.bucket, s.objectKey(key))
}

func (s *s3PayloadStore) ReadRecord(ctx context.Context, record *Record) ([]byte, error) {
	if record == nil {
		return nil, fmt.Errorf("artifact record is nil")
	}
	return s.client.Get(ctx, s.bucketForRecord(record), s.objectKey(record.StorageKey))
}

func (s *s3PayloadStore) Delete(ctx context.Context, key string) error {
	return s.client.Delete(ctx, s.bucket, s.objectKey(key))
}

func (s *s3PayloadStore) DeleteRecord(ctx context.Context, record *Record) error {
	if record == nil {
		return fmt.Errorf("artifact record is nil")
	}
	return s.client.Delete(ctx, s.bucketForRecord(record), s.objectKey(record.StorageKey))
}

func (s *s3PayloadStore) objectKey(key string) string {
	key = normalizeStorageKey(key)
	if s.prefix == "" {
		return key
	}
	return strings.TrimPrefix(s.prefix+"/"+key, "/")
}

func (s *s3PayloadStore) bucketForRecord(record *Record) string {
	if record == nil {
		return s.bucket
	}
	return firstNonEmpty(record.StorageContainer, s.bucket)
}

type minioObjectClient struct {
	client *minio.Client
}

func newMinioObjectClient(cfg Config) (*minioObjectClient, error) {
	endpoint := strings.TrimSpace(cfg.PayloadS3Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("artifact payload s3 endpoint is empty")
	}
	if strings.TrimSpace(cfg.PayloadS3AccessKey) == "" || strings.TrimSpace(cfg.PayloadS3SecretKey) == "" {
		return nil, fmt.Errorf("artifact payload s3 credentials are incomplete")
	}
	region := firstNonEmpty(cfg.PayloadS3Region, "us-east-1")
	options := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.PayloadS3AccessKey, cfg.PayloadS3SecretKey, cfg.PayloadS3SessionToken),
		Secure: !cfg.PayloadS3Insecure,
		Region: region,
	}
	if cfg.PayloadS3PathStyle {
		options.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, options)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bucket := strings.TrimSpace(cfg.PayloadS3Bucket)
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); err != nil {
			return nil, err
		}
	}
	return &minioObjectClient{client: client}, nil
}

func (c *minioObjectClient) Put(ctx context.Context, bucket, key string, payload []byte) error {
	_, err := c.client.PutObject(ctx, bucket, key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{})
	return err
}

func (c *minioObjectClient) Get(ctx context.Context, bucket, key string) ([]byte, error) {
	object, err := c.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrPayloadNotFound
		}
		return nil, err
	}
	defer object.Close()
	if _, err := object.Stat(); err != nil {
		if isS3NotFound(err) {
			return nil, ErrPayloadNotFound
		}
		return nil, err
	}
	payload, err := io.ReadAll(object)
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrPayloadNotFound
		}
		return nil, err
	}
	return payload, nil
}

func (c *minioObjectClient) Delete(ctx context.Context, bucket, key string) error {
	err := c.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
	if isS3NotFound(err) {
		return ErrPayloadNotFound
	}
	return err
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrPayloadNotFound) {
		return true
	}
	resp := minio.ToErrorResponse(err)
	switch resp.Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return true
	}
	return false
}
