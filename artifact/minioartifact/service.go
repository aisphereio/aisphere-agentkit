// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package minioartifact provides a MinIO/S3-backed [artifact.Service].
package minioartifact

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"google.golang.org/genai"

	"google.golang.org/adk/artifact"
)

const (
	userScopedArtifactKey    = "user"
	userScopedArtifactAppKey = "__user__"
	recordContentType        = "application/json"
)

// Config describes a MinIO/S3 artifact backend.
type Config struct {
	Endpoint        string
	Bucket          string
	AccessKey       string
	SecretKey       string
	Region          string
	UseSSL          bool
	Prefix          string
	CreateBucket    bool
	LookupPathStyle bool
}

// Service stores artifact versions as JSON records in MinIO/S3.
type Service struct {
	mu     sync.Mutex
	client *minio.Client
	cfg    Config
	prefix string
}

type artifactRecord struct {
	Version      int64          `json:"version"`
	CanonicalURI string         `json:"canonical_uri,omitempty"`
	CreateTime   float64        `json:"create_time"`
	Part         *genai.Part    `json:"part"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// NewService creates a MinIO/S3-backed artifact service.
func NewService(ctx context.Context, cfg Config) (artifact.Service, error) {
	cfg.Endpoint = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(cfg.Endpoint, "http://"), "https://"))
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Prefix = strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("minio artifact endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("minio artifact bucket is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("minio artifact access_key and secret_key are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: bucketLookupType(cfg.LookupPathStyle),
	})
	if err != nil {
		return nil, fmt.Errorf("create minio artifact client: %w", err)
	}
	if cfg.CreateBucket {
		exists, err := client.BucketExists(ctx, cfg.Bucket)
		if err != nil {
			return nil, fmt.Errorf("check minio artifact bucket %q: %w", cfg.Bucket, err)
		}
		if !exists {
			if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
				return nil, fmt.Errorf("create minio artifact bucket %q: %w", cfg.Bucket, err)
			}
		}
	}
	return &Service{client: client, cfg: cfg, prefix: cfg.Prefix}, nil
}

func bucketLookupType(pathStyle bool) minio.BucketLookupType {
	if pathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}

// Save implements [artifact.Service].
func (s *Service) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := scope(req.AppName, req.UserID, req.SessionID, req.FileName)

	s.mu.Lock()
	defer s.mu.Unlock()

	version := req.Version
	if version <= 0 {
		versions, err := s.versions(ctx, appName, userID, sessionID, fileName)
		if err != nil {
			return nil, err
		}
		version = int64(1)
		if len(versions) > 0 {
			version = versions[len(versions)-1] + 1
		}
	}
	key := s.versionKey(appName, userID, sessionID, fileName, version)
	rec := artifactRecord{
		Version:      version,
		CanonicalURI: fmt.Sprintf("s3://%s/%s", s.cfg.Bucket, key),
		CreateTime:   float64(time.Now().UnixNano()) / 1e9,
		Part:         req.Part,
	}
	data, err := json.MarshalIndent(&rec, "", "  ")
	if err != nil {
		return nil, err
	}
	_, err = s.client.PutObject(ctx, s.cfg.Bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: recordContentType,
		UserMetadata: map[string]string{
			"artifact-mime-type": mimeType(req.Part),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("save minio artifact %q: %w", key, err)
	}
	return &artifact.SaveResponse{Version: version}, nil
}

// Load implements [artifact.Service].
func (s *Service) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	rec, err := s.readRecordForRequest(ctx, req.AppName, req.UserID, req.SessionID, req.FileName, req.Version)
	if err != nil {
		return nil, err
	}
	return &artifact.LoadResponse{Part: rec.Part}, nil
}

// Delete implements [artifact.Service].
func (s *Service) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := scope(req.AppName, req.UserID, req.SessionID, req.FileName)
	if req.Version > 0 {
		return s.removeObject(ctx, s.versionKey(appName, userID, sessionID, fileName, req.Version))
	}
	versions, err := s.versions(ctx, appName, userID, sessionID, fileName)
	if err != nil {
		return nil
	}
	for _, version := range versions {
		if err := s.removeObject(ctx, s.versionKey(appName, userID, sessionID, fileName, version)); err != nil {
			return err
		}
	}
	return nil
}

// List implements [artifact.Service].
func (s *Service) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	files := map[string]bool{}
	for _, prefix := range []string{
		s.sessionPrefix(req.AppName, req.UserID, req.SessionID),
		s.sessionPrefix(userScopedArtifactAppKey, req.UserID, userScopedArtifactKey),
		s.sessionPrefix(req.AppName, req.UserID, userScopedArtifactKey),
	} {
		if err := s.collectFileNames(ctx, prefix, files); err != nil {
			return nil, err
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return &artifact.ListResponse{FileNames: names}, nil
}

// Versions implements [artifact.Service].
func (s *Service) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	if fileHasUserNamespace(req.FileName) {
		for _, appName := range []string{userScopedArtifactAppKey, req.AppName} {
			versions, err := s.versions(ctx, appName, req.UserID, userScopedArtifactKey, req.FileName)
			if err == nil && len(versions) > 0 {
				return &artifact.VersionsResponse{Versions: versions}, nil
			}
		}
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	versions, err := s.versions(ctx, req.AppName, req.UserID, req.SessionID, req.FileName)
	if err != nil || len(versions) == 0 {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &artifact.VersionsResponse{Versions: versions}, nil
}

// GetArtifactVersion implements [artifact.Service].
func (s *Service) GetArtifactVersion(ctx context.Context, req *artifact.GetArtifactVersionRequest) (*artifact.GetArtifactVersionResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	rec, err := s.readRecordForRequest(ctx, req.AppName, req.UserID, req.SessionID, req.FileName, req.Version)
	if err != nil {
		return nil, err
	}
	return &artifact.GetArtifactVersionResponse{
		ArtifactVersion: &artifact.ArtifactVersion{
			Version:        rec.Version,
			CanonicalURI:   rec.CanonicalURI,
			CustomMetadata: rec.Metadata,
			CreateTime:     rec.CreateTime,
			MimeType:       mimeType(rec.Part),
		},
	}, nil
}

func (s *Service) readRecordForRequest(ctx context.Context, appName, userID, sessionID, fileName string, version int64) (*artifactRecord, error) {
	if fileHasUserNamespace(fileName) {
		for _, candidateApp := range []string{userScopedArtifactAppKey, appName} {
			rec, err := s.readRecord(ctx, candidateApp, userID, userScopedArtifactKey, fileName, version)
			if err == nil {
				return rec, nil
			}
		}
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return s.readRecord(ctx, appName, userID, sessionID, fileName, version)
}

func (s *Service) readRecord(ctx context.Context, appName, userID, sessionID, fileName string, version int64) (*artifactRecord, error) {
	if version <= 0 {
		versions, err := s.versions(ctx, appName, userID, sessionID, fileName)
		if err != nil || len(versions) == 0 {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		version = versions[len(versions)-1]
	}
	key := s.versionKey(appName, userID, sessionID, fileName, version)
	obj, err := s.client.GetObject(ctx, s.cfg.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("load minio artifact %q: %w", key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return nil, fmt.Errorf("read minio artifact %q: %w", key, err)
	}
	var rec artifactRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode minio artifact %q: %w", key, err)
	}
	if rec.CanonicalURI == "" {
		rec.CanonicalURI = fmt.Sprintf("s3://%s/%s", s.cfg.Bucket, key)
	}
	return &rec, nil
}

func (s *Service) versions(ctx context.Context, appName, userID, sessionID, fileName string) ([]int64, error) {
	prefix := s.filePrefix(appName, userID, sessionID, fileName)
	var versions []int64
	for object := range s.client.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, fmt.Errorf("list minio artifacts %q: %w", prefix, object.Err)
		}
		version, ok := parseVersionObject(prefix, object.Key)
		if ok {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

func (s *Service) collectFileNames(ctx context.Context, prefix string, files map[string]bool) error {
	for object := range s.client.ListObjects(ctx, s.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return fmt.Errorf("list minio artifact prefix %q: %w", prefix, object.Err)
		}
		name, ok := parseFileObject(prefix, object.Key)
		if ok && (!fileHasUserNamespace(name) || strings.Contains(prefix, "/"+encodeSegment(userScopedArtifactKey)+"/")) {
			files[name] = true
		}
	}
	return nil
}

func (s *Service) removeObject(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		return fmt.Errorf("delete minio artifact %q: %w", key, err)
	}
	return nil
}

func parseFileObject(prefix, key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", false
	}
	segments := strings.Split(rest, "/")
	if len(segments) < 2 {
		return "", false
	}
	name, err := decodeSegment(segments[0])
	return name, err == nil
}

func parseVersionObject(prefix, key string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return 0, false
	}
	rest = strings.TrimSuffix(rest, ".json")
	version, err := strconv.ParseInt(strings.Trim(rest, "/"), 10, 64)
	return version, err == nil && version > 0
}

func scope(appName, userID, sessionID, fileName string) (string, string, string, string) {
	if fileHasUserNamespace(fileName) {
		return userScopedArtifactAppKey, userID, userScopedArtifactKey, fileName
	}
	return appName, userID, sessionID, fileName
}

func (s *Service) versionKey(appName, userID, sessionID, fileName string, version int64) string {
	return s.filePrefix(appName, userID, sessionID, fileName) + fmt.Sprintf("%d.json", version)
}

func (s *Service) filePrefix(appName, userID, sessionID, fileName string) string {
	return s.sessionPrefix(appName, userID, sessionID) + encodeSegment(fileName) + "/"
}

func (s *Service) sessionPrefix(appName, userID, sessionID string) string {
	parts := []string{s.prefix, encodeSegment(appName), encodeSegment(userID), encodeSegment(sessionID)}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "/") + "/"
}

func fileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}

func encodeSegment(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func decodeSegment(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mimeType(part *genai.Part) string {
	if part == nil {
		return "application/octet-stream"
	}
	if part.InlineData != nil && part.InlineData.MIMEType != "" {
		return part.InlineData.MIMEType
	}
	return "text/plain; charset=utf-8"
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket" || resp.StatusCode == 404
}

var _ artifact.Service = (*Service)(nil)
