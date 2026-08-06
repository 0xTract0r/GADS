package router

import (
	"GADS/common/models"
	"GADS/provider/devices"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type typingTestLogger struct {
	mu     sync.Mutex
	infos  []string
	errors []string
}

func (l *typingTestLogger) LogDebug(eventName string, message string) {}
func (l *typingTestLogger) LogInfo(eventName string, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, eventName+" "+message)
}
func (l *typingTestLogger) LogError(eventName string, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, eventName+" "+message)
}
func (l *typingTestLogger) LogWarn(eventName string, message string)  {}
func (l *typingTestLogger) LogFatal(eventName string, message string) {}
func (l *typingTestLogger) LogPanic(eventName string, message string) {}

func (l *typingTestLogger) infoMessages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.infos...)
}

func TestTypingCoalescerResetsIdleDeadlineForNewCharacters(t *testing.T) {
	calls := make(chan []string, 2)
	setTypingDownstreamForTest(t, func(dev devices.PlatformDevice, chars []string) (*http.Response, error) {
		calls <- append([]string(nil), chars...)
		return typingSuccessResponse(), nil
	})

	typer := newTypingTestTyper(200*time.Millisecond, time.Second, 64)
	if _, err := typer.submit("a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := typer.submit("b"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
	if _, err := typer.submit("c"); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-calls:
		if got := strings.Join(batch, ""); got != "abc" {
			t.Fatalf("first batch = %q, want %q", got, "abc")
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("timed out waiting for idle-reset batch")
	}
}

func TestTypingCoalescerHonorsHardDeadlineWhileInputStaysActive(t *testing.T) {
	type observedBatch struct {
		chars    []string
		calledAt time.Time
	}
	calls := make(chan observedBatch, 1)
	setTypingDownstreamForTest(t, func(dev devices.PlatformDevice, chars []string) (*http.Response, error) {
		calls <- observedBatch{chars: append([]string(nil), chars...), calledAt: time.Now()}
		return typingSuccessResponse(), nil
	})

	typer := newTypingTestTyper(200*time.Millisecond, 120*time.Millisecond, 64)
	startedAt := time.Now()
	for _, char := range []string{"a", "b", "c", "d"} {
		if _, err := typer.submit(char); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	select {
	case batch := <-calls:
		if got := strings.Join(batch.chars, ""); got != "abcd" {
			t.Fatalf("hard-deadline batch = %q, want %q", got, "abcd")
		}
		elapsed := batch.calledAt.Sub(startedAt)
		if elapsed < 100*time.Millisecond || elapsed > 190*time.Millisecond {
			t.Fatalf("hard-deadline batch flushed after %s, want approximately 120ms", elapsed)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("hard deadline did not flush continuously active input")
	}
}

func TestTypingCoalescerFlushesImmediatelyAtMaxBatchAndLogsTiming(t *testing.T) {
	calls := make(chan []string, 1)
	setTypingDownstreamForTest(t, func(dev devices.PlatformDevice, chars []string) (*http.Response, error) {
		calls <- append([]string(nil), chars...)
		return typingSuccessResponse(), nil
	})

	logger := &typingTestLogger{}
	typer := newTypingTestTyperWithLogger(logger, time.Second, 2*time.Second, 2)
	if _, err := typer.submit("你🙂"); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-calls:
		if got := strings.Join(batch, ""); got != "你🙂" {
			t.Fatalf("batch = %q, want %q", got, "你🙂")
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("max-size batch did not flush immediately")
	}

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		messages := logger.infoMessages()
		if len(messages) > 0 {
			message := messages[0]
			for _, field := range []string{"typing_latency", "batch_size=2", "queue_ms=", "wda_ms=", "total_ms=", "pending=0", "status=200"} {
				if !strings.Contains(message, field) {
					t.Fatalf("timing log %q does not contain %q", message, field)
				}
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timing log was not emitted")
}

func TestTypingCoalescerPreservesOverdueCharactersWhileWDAIsInFlight(t *testing.T) {
	calls := make(chan []string, 2)
	releaseFirst := make(chan struct{})
	callNumber := 0
	var callMu sync.Mutex
	setTypingDownstreamForTest(t, func(dev devices.PlatformDevice, chars []string) (*http.Response, error) {
		callMu.Lock()
		callNumber++
		currentCall := callNumber
		callMu.Unlock()
		calls <- append([]string(nil), chars...)
		if currentCall == 1 {
			<-releaseFirst
		}
		return typingSuccessResponse(), nil
	})

	typer := newTypingTestTyper(20*time.Millisecond, 80*time.Millisecond, 64)
	if _, err := typer.submit("ab"); err != nil {
		t.Fatal(err)
	}
	if batch := waitTypingBatch(t, calls, 150*time.Millisecond); strings.Join(batch, "") != "ab" {
		t.Fatalf("first batch = %q, want %q", strings.Join(batch, ""), "ab")
	}

	if _, err := typer.submit("c"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := typer.submit("d"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(90 * time.Millisecond)

	releasedAt := time.Now()
	close(releaseFirst)
	second := waitTypingBatch(t, calls, 50*time.Millisecond)
	if got := strings.Join(second, ""); got != "cd" {
		t.Fatalf("second batch = %q, want %q", got, "cd")
	}
	if elapsed := time.Since(releasedAt); elapsed > 50*time.Millisecond {
		t.Fatalf("overdue pending batch waited %s after WDA became free", elapsed)
	}
}

func setTypingDownstreamForTest(t *testing.T, downstream func(dev devices.PlatformDevice, chars []string) (*http.Response, error)) {
	t.Helper()
	previous := typingDownstream
	typingDownstream = downstream
	t.Cleanup(func() {
		typingDownstream = previous
	})
}

func newTypingTestTyper(idleWait, maxWait time.Duration, maxBatch int) *deviceTyper {
	return newTypingTestTyperWithLogger(&typingTestLogger{}, idleWait, maxWait, maxBatch)
}

func newTypingTestTyperWithLogger(logger models.CustomLogger, idleWait, maxWait time.Duration, maxBatch int) *deviceTyper {
	dev := &devices.IOSDevice{RuntimeState: devices.RuntimeState{
		DBDevice: models.DBDevice{UDID: "typing-test-device", OS: "ios"},
		Context:  context.Background(),
		Logger:   logger,
	}}
	typer := newDeviceTyper(dev)
	typer.idleWait = idleWait
	typer.maxWait = maxWait
	typer.maxBatch = maxBatch
	return typer
}

func typingSuccessResponse() *http.Response {
	return buildSyntheticResponse(http.StatusOK, nil, []byte(`{"value":null}`))
}

func waitTypingBatch(t *testing.T, calls <-chan []string, timeout time.Duration) []string {
	t.Helper()
	select {
	case batch := <-calls:
		return batch
	case <-time.After(timeout):
		t.Fatal("timed out waiting for typing batch")
		return nil
	}
}
