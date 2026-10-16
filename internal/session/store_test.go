package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/Jacksonoseikojo1122/ussd-runtime/internal/engine"
)

func stores(t *testing.T) map[string]Store {
	mr := miniredis.RunT(t)
	r, err := NewRedis("redis://"+mr.Addr(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Store{"memory": NewMemory(time.Minute), "miniredis": r}
	if url := os.Getenv("TEST_REDIS_URL"); url != "" {
		real, err := NewRedis(url, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		m["redis"] = real
	}
	return m
}

func TestStores(t *testing.T) {
	ctx := context.Background()
	for name, st := range stores(t) {
		t.Run(name, func(t *testing.T) {
			key := "test:" + name + time.Now().Format("150405.000000")
			if s, err := st.Get(ctx, key); s != nil || err != nil {
				t.Fatalf("empty get: %v %v", s, err)
			}
			in := engine.NewSession("abc", "nalo", "233241234567", "MTN")
			in.Screen, in.Vars["amount"] = "pay", "10.00"
			if err := st.Put(ctx, key, in); err != nil {
				t.Fatal(err)
			}
			out, err := st.Get(ctx, key)
			if err != nil || out == nil || out.Screen != "pay" || out.Vars["amount"] != "10.00" || out.Phone != "233241234567" {
				t.Fatalf("round trip: %+v %v", out, err)
			}
			out.Vars["amount"] = "changed"
			again, _ := st.Get(ctx, key)
			if again.Vars["amount"] != "10.00" {
				t.Fatal("Get must return a copy")
			}
			if err := st.Delete(ctx, key); err != nil {
				t.Fatal(err)
			}
			if s, _ := st.Get(ctx, key); s != nil {
				t.Fatal("still there after delete")
			}
			if err := st.Ping(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExpiry(t *testing.T) {
	ctx := context.Background()
	m := NewMemory(20 * time.Millisecond)
	_ = m.Put(ctx, "k", engine.NewSession("a", "x", "", ""))
	time.Sleep(40 * time.Millisecond)
	if s, _ := m.Get(ctx, "k"); s != nil {
		t.Fatal("memory session should expire")
	}

	mr := miniredis.RunT(t)
	r, _ := NewRedis("redis://"+mr.Addr(), time.Minute)
	_ = r.Put(ctx, "k", engine.NewSession("a", "x", "", ""))
	if ttl := mr.TTL(r.Prefix + "k"); ttl != time.Minute {
		t.Fatalf("redis TTL %v", ttl)
	}
	mr.FastForward(2 * time.Minute)
	if s, _ := r.Get(ctx, "k"); s != nil {
		t.Fatal("redis session should expire")
	}
}
