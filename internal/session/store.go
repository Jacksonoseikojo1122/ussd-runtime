// Package session stores USSD sessions between requests: in memory for one instance,
// or in Redis when several instances share the work.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
)

type Store interface {
	Get(ctx context.Context, key string) (*engine.Session, error) // nil, nil if there's none
	Put(ctx context.Context, key string, s *engine.Session) error
	Delete(ctx context.Context, key string) error
	Ping(ctx context.Context) error
}

// --- Memory -----------------------------------------------------------------

type Memory struct {
	TTL time.Duration
	mu  sync.Mutex
	m   map[string]memEntry
}

type memEntry struct {
	data    []byte
	expires time.Time
}

func NewMemory(ttl time.Duration) *Memory {
	m := &Memory{TTL: ttl, m: map[string]memEntry{}}
	go m.sweep()
	return m
}

func (m *Memory) Get(_ context.Context, key string) (*engine.Session, error) {
	m.mu.Lock()
	e, ok := m.m[key]
	m.mu.Unlock()
	if !ok || time.Now().After(e.expires) {
		return nil, nil
	}
	var s engine.Session
	return &s, json.Unmarshal(e.data, &s) // a copy, like Redis, so callers can't share state by accident
}

func (m *Memory) Put(_ context.Context, key string, s *engine.Session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.m[key] = memEntry{data, time.Now().Add(m.TTL)}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	delete(m.m, key)
	m.mu.Unlock()
	return nil
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.m)
}

func (m *Memory) sweep() {
	for range time.Tick(30 * time.Second) {
		now := time.Now()
		m.mu.Lock()
		for k, e := range m.m {
			if now.After(e.expires) {
				delete(m.m, k)
			}
		}
		m.mu.Unlock()
	}
}

// --- Redis ------------------------------------------------------------------

type Redis struct {
	Client *redis.Client
	TTL    time.Duration
	Prefix string
}

// NewRedis connects with a URL like redis://default:password@host:6379/0 (rediss:// for TLS).
func NewRedis(url string, ttl time.Duration) (*Redis, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Redis{Client: redis.NewClient(opt), TTL: ttl, Prefix: "ussd:session:"}, nil
}

func (r *Redis) Get(ctx context.Context, key string) (*engine.Session, error) {
	data, err := r.Client.Get(ctx, r.Prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s engine.Session
	return &s, json.Unmarshal(data, &s)
}

func (r *Redis) Put(ctx context.Context, key string, s *engine.Session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.Client.Set(ctx, r.Prefix+key, data, r.TTL).Err()
}

func (r *Redis) Delete(ctx context.Context, key string) error {
	return r.Client.Del(ctx, r.Prefix+key).Err()
}

func (r *Redis) Ping(ctx context.Context) error { return r.Client.Ping(ctx).Err() }
