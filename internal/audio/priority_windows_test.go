//go:build windows

package audio

import (
	"strings"
	"testing"
)

func TestLockAudioThread_RaisesPriority(t *testing.T) {
	result := make(chan string)
	go func() {
		result <- LockAudioThread() // la goroutine termina y su hilo muere con ella
	}()
	got := <-result
	if strings.HasPrefix(got, "prioridad normal") {
		t.Fatalf("audio thread kept normal priority: %s", got)
	}
	t.Logf("hilo de envío con %s", got)
}
