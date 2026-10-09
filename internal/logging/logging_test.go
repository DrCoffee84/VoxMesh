package logging

import (
	"sync"
	"testing"
)

func TestLogLevels(t *testing.T) {
	SetLevel(LevelError)
	if GetLevel() != LevelError {
		t.Fatalf("expected level %d, got %d", LevelError, GetLevel())
	}
	if IsControl() {
		t.Fatalf("expected IsControl to be false at LevelError")
	}
	if IsTrace() {
		t.Fatalf("expected IsTrace to be false at LevelError")
	}

	SetLevel(LevelControl)
	if GetLevel() != LevelControl {
		t.Fatalf("expected level %d, got %d", LevelControl, GetLevel())
	}
	if !IsControl() {
		t.Fatalf("expected IsControl to be true at LevelControl")
	}
	if IsTrace() {
		t.Fatalf("expected IsTrace to be false at LevelControl")
	}

	SetLevel(LevelTrace)
	if GetLevel() != LevelTrace {
		t.Fatalf("expected level %d, got %d", LevelTrace, GetLevel())
	}
	if !IsControl() {
		t.Fatalf("expected IsControl to be true at LevelTrace")
	}
	if !IsTrace() {
		t.Fatalf("expected IsTrace to be true at LevelTrace")
	}

	// Clamp test
	SetLevel(-5)
	if GetLevel() != LevelError {
		t.Fatalf("expected clamped level %d, got %d", LevelError, GetLevel())
	}
	SetLevel(99)
	if GetLevel() != LevelTrace {
		t.Fatalf("expected clamped level %d, got %d", LevelTrace, GetLevel())
	}
}

func TestTracedRWMutex(t *testing.T) {
	SetLevel(LevelTrace)
	mu := NewTracedRWMutex("testMu")

	mu.Lock()
	mu.Unlock()

	mu.RLock()
	mu.RUnlock()

	if !mu.TryLock() {
		t.Fatalf("expected TryLock to succeed")
	}
	mu.Unlock()

	if !mu.TryRLock() {
		t.Fatalf("expected TryRLock to succeed")
	}
	mu.RUnlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.RLock()
			defer mu.RUnlock()
		}()
	}
	wg.Wait()
}

