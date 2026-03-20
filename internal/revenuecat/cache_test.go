package revenuecat

import (
	"testing"
	"time"
)

func TestCache_GetEmpty(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Minute)
	_, ok := c.Get()
	if ok {
		t.Error("Get() on empty cache returned ok=true, want false")
	}
}

func TestCache_SetAndGet(t *testing.T) {
	t.Parallel()
	c := newTTLCache[string](time.Minute)
	c.Set("hello")
	val, ok := c.Get()
	if !ok {
		t.Fatal("Get() returned ok=false after Set")
	}
	if val != "hello" {
		t.Errorf("Get() = %q, want %q", val, "hello")
	}
}

func TestCache_Expiry(t *testing.T) {
	t.Parallel()
	c := newTTLCache[int](time.Millisecond)
	c.Set(42)
	time.Sleep(5 * time.Millisecond)
	_, ok := c.Get()
	if ok {
		t.Error("Get() returned ok=true after TTL expired, want false")
	}
}

func TestCacheMap_GetEmpty(t *testing.T) {
	t.Parallel()
	c := newTTLCacheMap[int](time.Minute)
	_, ok := c.Get("missing")
	if ok {
		t.Error("Get() on empty cache map returned ok=true, want false")
	}
}

func TestCacheMap_SetAndGet(t *testing.T) {
	t.Parallel()
	c := newTTLCacheMap[string](time.Minute)
	c.Set("key1", "hello")
	c.Set("key2", "world")

	val, ok := c.Get("key1")
	if !ok {
		t.Fatal("Get(key1) returned ok=false after Set")
	}
	if val != "hello" {
		t.Errorf("Get(key1) = %q, want %q", val, "hello")
	}

	val2, ok := c.Get("key2")
	if !ok {
		t.Fatal("Get(key2) returned ok=false after Set")
	}
	if val2 != "world" {
		t.Errorf("Get(key2) = %q, want %q", val2, "world")
	}
}

func TestCacheMap_Expiry(t *testing.T) {
	t.Parallel()
	c := newTTLCacheMap[int](time.Millisecond)
	c.Set("key", 42)
	time.Sleep(5 * time.Millisecond)
	_, ok := c.Get("key")
	if ok {
		t.Error("Get() returned ok=true after TTL expired, want false")
	}
}
