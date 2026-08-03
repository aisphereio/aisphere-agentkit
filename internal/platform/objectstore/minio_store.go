package objectstore

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOConfig struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKey       string
	SecretKey       string
	UseSSL          bool
	Prefix          string
	CreateBucket    bool
	LookupPathStyle bool
}

type minioStore struct {
	client *minio.Client
	bucket string
	prefix string
}

func NewMinIOStore(ctx context.Context, cfg MinIOConfig) (Store, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("minio endpoint is required")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("minio bucket is required")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("minio access_key and secret_key are required")
	}
	opts := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	if cfg.LookupPathStyle {
		opts.BucketLookup = minio.BucketLookupPath
	}
	client, err := minio.New(cfg.Endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	s := &minioStore{client: client, bucket: cfg.Bucket, prefix: cleanPrefix(cfg.Prefix)}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check minio bucket: %w", err)
	}
	if !exists {
		if !cfg.CreateBucket {
			return nil, fmt.Errorf("minio bucket %q does not exist", cfg.Bucket)
		}
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("create minio bucket: %w", err)
		}
	}
	return s, nil
}

func (s *minioStore) Put(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) (*ObjectInfo, error) {
	objectName, finalKey, err := s.objectName(key)
	if err != nil {
		return nil, err
	}
	userMeta := map[string]string{}
	for k, v := range opts.Metadata {
		userMeta[k] = v
	}
	info, err := s.client.PutObject(ctx, s.bucket, objectName, r, size, minio.PutObjectOptions{ContentType: opts.ContentType, UserMetadata: userMeta})
	if err != nil {
		return nil, fmt.Errorf("put minio object %s: %w", finalKey, err)
	}
	return &ObjectInfo{Key: finalKey, Size: info.Size, ContentType: opts.ContentType, ETag: info.ETag, LastModified: time.Now()}, nil
}

func (s *minioStore) Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error) {
	objectName, finalKey, err := s.objectName(key)
	if err != nil {
		return nil, nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("get minio object %s: %w", finalKey, err)
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, nil, fmt.Errorf("stat minio object %s: %w", finalKey, err)
	}
	return obj, &ObjectInfo{Key: finalKey, Size: st.Size, ContentType: st.ContentType, ETag: st.ETag, LastModified: st.LastModified}, nil
}

func (s *minioStore) Delete(ctx context.Context, key string) error {
	objectName, _, err := s.objectName(key)
	if err != nil {
		return err
	}
	return s.client.RemoveObject(ctx, s.bucket, objectName, minio.RemoveObjectOptions{})
}

func (s *minioStore) DeletePrefix(ctx context.Context, prefix string) error {
	objectPrefix, _, err := s.objectName(prefix)
	if err != nil {
		return err
	}
	ch := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: ensureTrailingSlash(objectPrefix), Recursive: true})
	for obj := range ch {
		if obj.Err != nil {
			return obj.Err
		}
		if err := s.client.RemoveObject(ctx, s.bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil {
			return err
		}
	}
	return nil
}

func (s *minioStore) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	objectPrefix, _, err := s.objectName(prefix)
	if err != nil {
		return nil, err
	}
	var out []ObjectInfo
	ch := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: objectPrefix, Recursive: true})
	for obj := range ch {
		if obj.Err != nil {
			return nil, obj.Err
		}
		key := obj.Key
		if s.prefix != "" {
			key = strings.TrimPrefix(key, s.prefix+"/")
		}
		out = append(out, ObjectInfo{Key: key, Size: obj.Size, ETag: obj.ETag, LastModified: obj.LastModified})
	}
	return out, nil
}

func (s *minioStore) Exists(ctx context.Context, key string) (bool, error) {
	objectName, _, err := s.objectName(key)
	if err != nil {
		return false, err
	}
	_, err = s.client.StatObject(ctx, s.bucket, objectName, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	errResp := minio.ToErrorResponse(err)
	if errResp.Code == "NoSuchKey" || errResp.Code == "NoSuchObject" || errResp.StatusCode == 404 {
		return false, nil
	}
	return false, err
}

func (s *minioStore) objectName(key string) (string, string, error) {
	clean, err := cleanKey(key)
	if err != nil {
		return "", "", err
	}
	name := clean
	if s.prefix != "" {
		name = s.prefix + "/" + clean
	}
	name = filepath.ToSlash(name)
	return name, clean, nil
}

func ensureTrailingSlash(v string) string {
	if strings.HasSuffix(v, "/") {
		return v
	}
	return v + "/"
}
