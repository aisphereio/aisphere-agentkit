// Copyright 2026 Google LLC
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

// Package objectstore provides a small project-scoped object storage abstraction.
// It is intentionally below business services: agents and toolsets should call
// domain services such as novelstore instead of constructing object keys or
// touching MinIO/S3 credentials directly.
package objectstore

import (
	"context"
	"io"
	"time"
)

// Store is the minimal object storage contract needed by platform domain stores.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) (*ObjectInfo, error)
	Get(ctx context.Context, key string) (io.ReadCloser, *ObjectInfo, error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Exists(ctx context.Context, key string) (bool, error)
}

// PutOptions controls object metadata.
type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

// ObjectInfo is returned by object storage implementations.
type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}
