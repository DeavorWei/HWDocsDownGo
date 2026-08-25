package logger

import (
	"sync"
	"testing"
	"time"
)

func TestSafeGo(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	recovered := false
	SafeGo("test-panic-goroutine", func() {
		defer wg.Done()
		defer func() {
			recovered = true
		}()
		panic("simulated panic in test")
	})

	wg.Wait()
	time.Sleep(50 * time.Millisecond)

	if !recovered {
		t.Errorf("SafeGo should execute defer and recover gracefully without crashing")
	}
}
