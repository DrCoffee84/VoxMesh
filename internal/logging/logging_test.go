package logging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestCrashOutput relanza el binario de test como subproceso que hace panic en una goroutine
// sin recover: la traza tiene que terminar en el archivo de log aunque no haya stderr.
func TestCrashOutput(t *testing.T) {
	if os.Getenv("VOXMESH_CRASH_CHILD") == "1" {
		if err := Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			panic("crash de prueba")
		}()
		<-done
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutput$")
	cmd.Env = append(os.Environ(), "VOXMESH_CRASH_CHILD=1", "APPDATA="+dir, "XDG_CONFIG_HOME="+dir, "HOME="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("el subproceso tenía que morir por el panic")
	}
	logs, _ := filepath.Glob(filepath.Join(dir, "*", "VoxMesh", "log-*.log"))
	more, _ := filepath.Glob(filepath.Join(dir, "VoxMesh", "log-*.log"))
	logs = append(logs, more...)
	if len(logs) != 1 {
		t.Fatalf("esperaba un archivo de log, encontré %v", logs)
	}
	data, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"=== inicio VoxMesh", "panic: crash de prueba", "goroutine "} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("el log no contiene %q:\n%s", want, data)
		}
	}
}
