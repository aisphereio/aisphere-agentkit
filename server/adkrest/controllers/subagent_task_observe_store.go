package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"google.golang.org/adk/internal/runtimeconfig"
)

// SubAgentTaskObserveStore keeps short-lived runtime observation events for UI recovery.
//
// It is intentionally not a formal session store. The data here is runtime/debug
// observation state used to restore the live run monitor after a browser refresh.
// It must not be fed back into model context.
//
// NOTE: the historical name says SubAgentTask because the first UI consumer was
// the sub-agent task card. The store now records every runtime.log event that is
// selected by runtimeLogSSERecorder, including model budget checks, token usage,
// tools and skill injection. Keeping the old type name avoids a large mechanical
// rename across server wiring.
type SubAgentTaskObserveStore interface {
	Record(payload map[string]any)
	List(ctx context.Context, appName, userID, sessionID string) []map[string]any
	DeleteSession(ctx context.Context, appName, userID, sessionID string)
}

const maxSubAgentObserveEventsPerSession = 20000

type subAgentTaskObserveEvent struct {
	at      time.Time
	payload map[string]any
}

type memorySubAgentTaskObserveStore struct {
	ttl time.Duration

	mu     sync.Mutex
	events map[string][]subAgentTaskObserveEvent
	seen   map[string]map[string]struct{}
}

type redisSubAgentTaskObserveStore struct {
	client    redis.UniversalClient
	keyPrefix string
	ttl       time.Duration
}

// NewSubAgentTaskObserveStore creates an in-memory TTL store for sub-agent task
// observation events. Use NewBestEffortSubAgentTaskObserveStore when runtime
// Redis configuration is available.
func NewSubAgentTaskObserveStore(ttl time.Duration) SubAgentTaskObserveStore {
	return NewMemorySubAgentTaskObserveStore(ttl)
}

func NewMemorySubAgentTaskObserveStore(ttl time.Duration) SubAgentTaskObserveStore {
	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	return &memorySubAgentTaskObserveStore{
		ttl:    ttl,
		events: make(map[string][]subAgentTaskObserveEvent),
		seen:   make(map[string]map[string]struct{}),
	}
}

func NewBestEffortSubAgentTaskObserveStore(ctx context.Context, cfg runtimeconfig.ResumableRunsConfig, ttl time.Duration) (SubAgentTaskObserveStore, error) {
	redisStore, err := NewRedisSubAgentTaskObserveStore(ctx, cfg, ttl)
	if err != nil {
		return nil, err
	}
	if redisStore != nil {
		return redisStore, nil
	}
	return NewMemorySubAgentTaskObserveStore(ttl), nil
}

func NewRedisSubAgentTaskObserveStore(ctx context.Context, cfg runtimeconfig.ResumableRunsConfig, ttl time.Duration) (SubAgentTaskObserveStore, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.Addrs) == 0 {
		return nil, errors.New("subagent observe redis addrs are required")
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
		return nil, fmt.Errorf("connect redis for subagent observe store: %w", err)
	}

	if ttl <= 0 {
		ttl = 6 * time.Hour
	}
	keyPrefix := strings.TrimSpace(cfg.KeyPrefix)
	if keyPrefix == "" {
		keyPrefix = "adk:run"
	}
	return &redisSubAgentTaskObserveStore{
		client:    client,
		keyPrefix: keyPrefix + ":subagent_observe",
		ttl:       ttl,
	}, nil
}

func (s *redisSubAgentTaskObserveStore) Record(payload map[string]any) {
	if s == nil || len(payload) == 0 {
		return
	}
	sessionID := strings.TrimSpace(anyString(payload["session_id"]))
	if sessionID == "" {
		return
	}
	cloned := cloneMap(payload)
	if _, ok := cloned["time"]; !ok {
		cloned["time"] = time.Now().Format(time.RFC3339Nano)
	}
	if _, ok := cloned["event_id"]; !ok {
		cloned["event_id"] = runtimeLogEventID(cloned)
	}
	b, err := json.Marshal(cloned)
	if err != nil {
		return
	}
	ctx := context.Background()
	key := s.key(anyString(payload["app_name"]), anyString(payload["user_id"]), sessionID)
	pipe := s.client.Pipeline()
	if eventID := strings.TrimSpace(anyString(cloned["event_id"])); eventID != "" {
		pipe.LRem(ctx, key, 0, string(b))
		indexKey := key + ":ids"
		if added := s.client.SAdd(ctx, indexKey, eventID).Val(); added == 0 {
			return
		}
		pipe.Expire(ctx, indexKey, s.ttl)
	}
	pipe.RPush(ctx, key, b)
	pipe.LTrim(ctx, key, -maxSubAgentObserveEventsPerSession, -1)
	pipe.Expire(ctx, key, s.ttl)
	_, _ = pipe.Exec(ctx)
}

func (s *redisSubAgentTaskObserveStore) List(ctx context.Context, appName, userID, sessionID string) []map[string]any {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	items, err := s.client.LRange(ctx, s.key(appName, userID, sessionID), 0, -1).Result()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		var payload map[string]any
		if err := json.Unmarshal([]byte(item), &payload); err != nil {
			continue
		}
		out = append(out, payload)
	}
	return out
}

func (s *redisSubAgentTaskObserveStore) DeleteSession(ctx context.Context, appName, userID, sessionID string) {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	key := s.key(appName, userID, sessionID)
	_ = s.client.Del(ctx, key, key+":ids").Err()
}

func (s *redisSubAgentTaskObserveStore) key(appName, userID, sessionID string) string {
	return s.keyPrefix + ":" + safeRedisKeyPart(appName) + ":" + safeRedisKeyPart(userID) + ":" + safeRedisKeyPart(sessionID)
}

// Record stores a single selected runtime observation payload for UI recovery.
func (s *memorySubAgentTaskObserveStore) Record(payload map[string]any) {
	if s == nil || len(payload) == 0 {
		return
	}
	appName := strings.TrimSpace(anyString(payload["app_name"]))
	userID := strings.TrimSpace(anyString(payload["user_id"]))
	sessionID := strings.TrimSpace(anyString(payload["session_id"]))
	if sessionID == "" {
		return
	}
	key := subAgentObserveKey(appName, userID, sessionID)
	now := time.Now()
	cloned := cloneMap(payload)
	if _, ok := cloned["time"]; !ok {
		cloned["time"] = now.Format(time.RFC3339Nano)
	}
	if _, ok := cloned["event_id"]; !ok {
		cloned["event_id"] = runtimeLogEventID(cloned)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	if eventID := strings.TrimSpace(anyString(cloned["event_id"])); eventID != "" {
		if s.seen[key] == nil {
			s.seen[key] = map[string]struct{}{}
		}
		if _, exists := s.seen[key][eventID]; exists {
			return
		}
		s.seen[key][eventID] = struct{}{}
	}
	items := append(s.events[key], subAgentTaskObserveEvent{at: now, payload: cloned})
	if len(items) > maxSubAgentObserveEventsPerSession {
		items = items[len(items)-maxSubAgentObserveEventsPerSession:]
		s.rebuildSeenLocked(key, items)
	}
	s.events[key] = items
}

// List returns non-expired runtime observation events for a session.
func (s *memorySubAgentTaskObserveStore) List(ctx context.Context, appName, userID, sessionID string) []map[string]any {
	_ = ctx
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	key := subAgentObserveKey(appName, userID, sessionID)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(now)
	items := s.events[key]
	if len(items) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	cutoff := now.Add(-s.ttl)
	kept := items[:0]
	for _, item := range items {
		if item.at.Before(cutoff) {
			continue
		}
		kept = append(kept, item)
		out = append(out, cloneMap(item.payload))
	}
	if len(kept) == 0 {
		delete(s.events, key)
		delete(s.seen, key)
	} else {
		s.events[key] = kept
		s.rebuildSeenLocked(key, kept)
	}
	return out
}

// DeleteSession deletes all observation events associated with a formal session.
func (s *memorySubAgentTaskObserveStore) DeleteSession(ctx context.Context, appName, userID, sessionID string) {
	_ = ctx
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, subAgentObserveKey(appName, userID, sessionID))
	delete(s.seen, subAgentObserveKey(appName, userID, sessionID))
}

func (s *memorySubAgentTaskObserveStore) pruneLocked(now time.Time) {
	if s == nil || s.ttl <= 0 {
		return
	}
	cutoff := now.Add(-s.ttl)
	for key, items := range s.events {
		kept := items[:0]
		for _, item := range items {
			if item.at.Before(cutoff) {
				continue
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			delete(s.events, key)
			delete(s.seen, key)
		} else {
			s.events[key] = kept
			s.rebuildSeenLocked(key, kept)
		}
	}
}

func (s *memorySubAgentTaskObserveStore) rebuildSeenLocked(key string, items []subAgentTaskObserveEvent) {
	if s == nil {
		return
	}
	if len(items) == 0 {
		delete(s.seen, key)
		return
	}
	ids := map[string]struct{}{}
	for _, item := range items {
		if eventID := strings.TrimSpace(anyString(item.payload["event_id"])); eventID != "" {
			ids[eventID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		delete(s.seen, key)
		return
	}
	s.seen[key] = ids
}

func subAgentObserveKey(appName, userID, sessionID string) string {
	return strings.TrimSpace(appName) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)
}

func safeRedisKeyPart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func anyString(v any) string {
	s, ok := v.(string)
	if ok {
		return s
	}
	return ""
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
