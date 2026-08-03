package objectstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/internal/runtimeconfig"
)

// Config is a concrete object store configuration. It mirrors runtimeconfig's
// storage.object fields plus credential fields used by MinIO/S3 compatible APIs.
type Config struct {
	Type         string
	Root         string
	Endpoint     string
	Bucket       string
	Region       string
	AccessKey    string
	AccessKeyEnv string
	SecretKey    string
	SecretKeyEnv string
	UseSSL       bool
	Prefix       string
	CreateBucket bool
	PathStyle    bool
}

// FromRuntimeConfig builds an object store from storage.object. Local filesystem
// is the default so development does not require MinIO.
func FromRuntimeConfig(ctx context.Context, cfg *runtimeconfig.Config) (Store, error) {
	if cfg == nil {
		cfg = runtimeconfig.FromContext(ctx)
	}
	oc := cfg.Storage.Object
	c := Config{
		Type:         oc.Type,
		Root:         oc.Root,
		Endpoint:     oc.Endpoint,
		Bucket:       oc.Bucket,
		Region:       oc.Region,
		AccessKey:    oc.AccessKey,
		AccessKeyEnv: oc.AccessKeyEnv,
		SecretKey:    oc.SecretKey,
		SecretKeyEnv: oc.SecretKeyEnv,
		UseSSL:       oc.UseSSL,
		Prefix:       oc.Prefix,
		CreateBucket: oc.CreateBucket,
		PathStyle:    oc.PathStyle,
	}
	return New(ctx, c)
}

// New constructs a Store from config.
func New(ctx context.Context, cfg Config) (Store, error) {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	switch typ {
	case "", "filesystem", "file", "fs", "localfs":
		root := strings.TrimSpace(cfg.Root)
		if root == "" {
			root = filepath.Join(".adk", "data", "objects")
		}
		return NewFilesystemStore(root, cfg.Prefix), nil
	case "minio", "s3", "s3_compatible", "s3-compatible":
		accessKey := firstNonEmpty(cfg.AccessKey, envValue(cfg.AccessKeyEnv))
		secretKey := firstNonEmpty(cfg.SecretKey, envValue(cfg.SecretKeyEnv))
		return NewMinIOStore(ctx, MinIOConfig{
			Endpoint:        cfg.Endpoint,
			Bucket:          cfg.Bucket,
			Region:          cfg.Region,
			AccessKey:       accessKey,
			SecretKey:       secretKey,
			UseSSL:          cfg.UseSSL,
			Prefix:          cfg.Prefix,
			CreateBucket:    cfg.CreateBucket,
			LookupPathStyle: cfg.PathStyle,
		})
	default:
		return nil, fmt.Errorf("unsupported object store type %q", cfg.Type)
	}
}

func envValue(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return os.Getenv(name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
