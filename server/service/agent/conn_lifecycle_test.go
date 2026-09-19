package agent

import (
	"sync"
	"testing"
)

// Connection replacement and read-loop teardown can call both stop methods at
// the same time. The stop signals must be idempotent and panic-free.
func TestAgentConnStopLoopsConcurrent(t *testing.T) {
	ac := newAgentConn(1, nil, "")

	const callers = 64
	var wg sync.WaitGroup
	wg.Add(callers * 2)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			ac.StopNoiseLoop()
		}()
		go func() {
			defer wg.Done()
			ac.StopWSPingLoop()
		}()
	}
	wg.Wait()

	select {
	case <-ac.noiseStop:
	default:
		t.Fatal("noise stop signal was not closed")
	}
	select {
	case <-ac.wsPingStop:
	default:
		t.Fatal("WebSocket ping stop signal was not closed")
	}

	// Repeated calls after closure must remain harmless.
	ac.StopNoiseLoop()
	ac.StopWSPingLoop()
}
