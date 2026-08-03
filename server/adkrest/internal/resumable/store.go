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

package resumable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/runtimetrace"
	"google.golang.org/adk/server/adkrest/internal/models"
)

const (
	KindData      = "data"
	KindDone      = "done"
	KindError     = "error"
	KindHeartbeat = "heartbeat"

	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
	statusCanceled  = "canceled"
)

type Store struct {
	client       redis.UniversalClient
	keyPrefix    string
	ttl          time.Duration
	blockTimeout time.Duration

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

type Message struct {
	ID   string
	Kind string
	Data string
}

type Producer func(ctx context.Context, emit func(string) error) error

func NewRedisStore(ctx context.Context, cfg runtimeconfig.ResumableRunsConfig) (*Store, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Addrs) == 0 {
		return nil, errors.New("resumable runs redis addrs are required")
	}

	password := cfg.Password
	if password == "" && cfg.PasswordEnv != "" {
		password = os.Getenv(cfg.PasswordEnv)
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "standalone"
	}
	var client redis.UniversalClient
	if mode == "cluster" {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Addrs,
			Username: cfg.Username,
			Password: password,
		})
	} else {
		client = redis.NewClient(&redis.Options{
			Addr:     cfg.Addrs[0],
			Username: cfg.Username,
			Password: password,
			DB:       cfg.DB,
		})
	}

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect redis for resumable runs: %w", err)
	}

	ttl, err := parseDurationDefault(cfg.TTL, 6*time.Hour)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("parse resumable runs ttl: %w", err)
	}
	blockTimeout, err := parseDurationDefault(cfg.BlockTimeout, 15*time.Second)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("parse resumable runs block_timeout: %w", err)
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = "adk:run"
	}

	return &Store{
		client:       client,
		keyPrefix:    keyPrefix,
		ttl:          ttl,
		blockTimeout: blockTimeout,
		running:      map[string]context.CancelFunc{},
	}, nil
}

func (s *Store) Start(req models.RunAgentRequest, producer Producer) (string, error) {
	runID := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	meta := map[string]any{
		"run_id":        runID,
		"app_name":      req.AppName,
		"user_id":       req.UserId,
		"session_id":    req.SessionId,
		"invocation_id": req.InvocationId,
		"status":        statusRunning,
		"created_at":    now,
		"updated_at":    now,
	}
	ctx := context.Background()
	if err := s.client.HSet(ctx, s.metaKey(runID), meta).Err(); err != nil {
		return "", fmt.Errorf("create run metadata: %w", err)
	}
	s.expire(ctx, runID)

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[runID] = cancel
	s.mu.Unlock()

	runCtx = runtimetrace.WithRunID(runCtx, runID)
	go s.run(runCtx, runID, producer)
	return runID, nil
}

func (s *Store) InvocationID(ctx context.Context, runID string) (string, error) {
	if s == nil {
		return "", nil
	}
	value, err := s.client.HGet(ctx, s.metaKey(runID), "invocation_id").Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return value, err
}

func (s *Store) run(ctx context.Context, runID string, producer Producer) {
	defer func() {
		s.mu.Lock()
		delete(s.running, runID)
		s.mu.Unlock()
	}()

	err := producer(ctx, func(data string) error {
		return s.append(ctx, runID, KindData, data)
	})
	switch {
	case err == nil:
		s.setStatus(context.Background(), runID, statusCompleted, "")
	case errors.Is(err, context.Canceled):
		s.setStatus(context.Background(), runID, statusCanceled, "")
	default:
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_ = s.append(context.Background(), runID, KindError, string(payload))
		s.setStatus(context.Background(), runID, statusFailed, err.Error())
	}
	_ = s.append(context.Background(), runID, KindDone, "{}")
}

func (s *Store) Stream(ctx context.Context, runID, cursor string, emit func(Message) error) error {
	if cursor == "" {
		cursor = "0-0"
	}
	key := s.streamKey(runID)
	for {
		streams, err := s.client.XRead(ctx, &redis.XReadArgs{
			Streams: []string{key, cursor},
			Count:   100,
			Block:   s.blockTimeout,
		}).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				if s.isTerminal(ctx, runID) {
					return nil
				}
				if err := emit(Message{Kind: KindHeartbeat, Data: "{}"}); err != nil {
					return err
				}
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return fmt.Errorf("read run stream: %w", err)
		}
		for _, stream := range streams {
			for _, xmsg := range stream.Messages {
				cursor = xmsg.ID
				msg := Message{
					ID:   xmsg.ID,
					Kind: stringValue(xmsg.Values["kind"]),
					Data: stringValue(xmsg.Values["data"]),
				}
				if msg.Kind == "" {
					msg.Kind = KindData
				}
				if msg.Kind == KindDone {
					if err := emit(msg); err != nil {
						return err
					}
					return nil
				}
				if err := emit(msg); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Store) Cancel(runID string) bool {
	s.mu.Lock()
	cancel := s.running[runID]
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Store) append(ctx context.Context, runID, kind, data string) error {
	if err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: s.streamKey(runID),
		Values: map[string]any{
			"kind": kind,
			"data": data,
		},
	}).Err(); err != nil {
		return fmt.Errorf("append run event: %w", err)
	}
	s.expire(context.Background(), runID)
	return nil
}

func (s *Store) setStatus(ctx context.Context, runID, status, errText string) {
	fields := map[string]any{
		"status":     status,
		"updated_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if errText != "" {
		fields["error"] = errText
	}
	_ = s.client.HSet(ctx, s.metaKey(runID), fields).Err()
	s.expire(ctx, runID)
}

func (s *Store) isTerminal(ctx context.Context, runID string) bool {
	status, err := s.client.HGet(ctx, s.metaKey(runID), "status").Result()
	if err != nil {
		return false
	}
	switch status {
	case statusCompleted, statusFailed, statusCanceled:
		return true
	default:
		return false
	}
}

func (s *Store) expire(ctx context.Context, runID string) {
	if s.ttl <= 0 {
		return
	}
	_ = s.client.Expire(ctx, s.streamKey(runID), s.ttl).Err()
	_ = s.client.Expire(ctx, s.metaKey(runID), s.ttl).Err()
}

func (s *Store) streamKey(runID string) string {
	return s.keyPrefix + ":{" + runID + "}:events"
}

func (s *Store) metaKey(runID string) string {
	return s.keyPrefix + ":{" + runID + "}:meta"
}

func parseDurationDefault(raw string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(value)
	}
}

func (s *Store) Exists(ctx context.Context, runID string) (bool, error) {
	n, err := s.client.Exists(ctx, s.metaKey(runID)).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
