package test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"cpa-usage-keeper/internal/quota"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestAutoRefreshTimerUnavailableFallsBackOnce(t *testing.T) {
	hook := logrustest.NewGlobal()
	t.Cleanup(hook.Reset)
	for _, timerErr := range []error{syscall.EINVAL, syscall.ENOSYS, syscall.EOPNOTSUPP, syscall.EPERM, syscall.EACCES} {
		t.Run(timerErr.Error(), func(t *testing.T) {
			hook.Reset()
			service := &quota.Service{}
			attempts := 0
			setAutoRefreshTimerFactory(service, func(time.Duration) (<-chan time.Time, func(), error) {
				attempts++
				return nil, nil, os.NewSyscallError("timerfd_create(CLOCK_BOOTTIME)", timerErr)
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for range 3 {
				start := time.Now()
				if reason := sleepAutoRefreshDelay(service, ctx, 5*time.Millisecond); reason != 0 {
					t.Fatalf("expected fallback timer to elapse, got wake reason %d", reason)
				}
				if time.Since(start) < 5*time.Millisecond {
					t.Fatal("fallback timer fired before the requested delay")
				}
			}
			if attempts != 1 {
				t.Fatalf("expected one capability probe, got %d", attempts)
			}
			assertSuspendAwareTimerWarnings(t, hook, 1)
		})
	}
}

func TestAutoRefreshTimerRetriesTransientFailures(t *testing.T) {
	hook := logrustest.NewGlobal()
	t.Cleanup(hook.Reset)
	for _, timerErr := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM} {
		t.Run(timerErr.Error(), func(t *testing.T) {
			hook.Reset()
			service := &quota.Service{}
			attempts, stops := 0, 0
			setAutoRefreshTimerFactory(service, func(delay time.Duration) (<-chan time.Time, func(), error) {
				attempts++
				if attempts == 1 {
					return nil, nil, os.NewSyscallError("timerfd_create(CLOCK_BOOTTIME)", timerErr)
				}
				timer := time.NewTimer(delay)
				return timer.C, func() {
					stops++
					timer.Stop()
				}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			for range 3 {
				if reason := sleepAutoRefreshDelay(service, ctx, time.Millisecond); reason != 0 {
					t.Fatalf("expected timer to elapse, got wake reason %d", reason)
				}
			}
			if attempts != 3 || stops != 2 {
				t.Fatalf("expected recovery and timer cleanup, got attempts=%d stops=%d", attempts, stops)
			}
			assertSuspendAwareTimerWarnings(t, hook, 1)
		})
	}
}

func TestAutoRefreshTimerFallbackCanBeInterrupted(t *testing.T) {
	service := &quota.Service{}
	wake := make(chan struct{}, 1)
	quotaServiceField(service, "autoRefreshSettingsChanged").Set(reflect.ValueOf(wake))
	attempts := 0
	setAutoRefreshTimerFactory(service, func(time.Duration) (<-chan time.Time, func(), error) {
		attempts++
		return nil, nil, syscall.EINVAL
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wake <- struct{}{}
	if reason := sleepAutoRefreshDelay(service, ctx, time.Hour); reason != 1 {
		t.Fatalf("expected settings change to interrupt fallback timer, got wake reason %d", reason)
	}
	cancel()
	if reason := sleepAutoRefreshDelay(service, ctx, time.Hour); reason != 2 {
		t.Fatalf("expected cancellation to interrupt fallback timer, got wake reason %d", reason)
	}
	if attempts != 1 {
		t.Fatalf("expected fallback to persist across interruptions, got %d attempts", attempts)
	}
}

func TestAutoRefreshTimerConcurrentFallbackProbesOnce(t *testing.T) {
	hook := logrustest.NewGlobal()
	t.Cleanup(hook.Reset)
	service := &quota.Service{}
	var attempts atomic.Int32
	setAutoRefreshTimerFactory(service, func(time.Duration) (<-chan time.Time, func(), error) {
		attempts.Add(1)
		return nil, nil, syscall.EINVAL
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var waits sync.WaitGroup
	for range 20 {
		waits.Go(func() {
			if reason := sleepAutoRefreshDelay(service, ctx, time.Hour); reason != 2 {
				t.Errorf("expected cancellation, got wake reason %d", reason)
			}
		})
	}
	waits.Wait()
	if attempts.Load() != 1 {
		t.Fatalf("expected one concurrent capability probe, got %d", attempts.Load())
	}
	assertSuspendAwareTimerWarnings(t, hook, 1)
}

func assertSuspendAwareTimerWarnings(t *testing.T, hook *logrustest.Hook, want int) {
	t.Helper()
	warnings := 0
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, "suspend-aware timer") {
			warnings++
		}
	}
	if warnings != want {
		t.Fatalf("expected %d timer warnings, got %d: %+v", want, warnings, hook.AllEntries())
	}
}
