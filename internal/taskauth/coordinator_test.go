package taskauth

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"litepan/internal/domain"
	"litepan/internal/eventbus"
)

type spyRunner struct {
	mu      sync.Mutex
	pauses  []pauseCall
	resumes []int64
}

type pauseCall struct {
	accountID int64
	reason    domain.PauseReason
	message   string
}

func (s *spyRunner) PauseByAccount(_ context.Context, accountID int64, reason domain.PauseReason, message string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pauses = append(s.pauses, pauseCall{accountID, reason, message})
	return 1, nil
}

func (s *spyRunner) ResumeByAccount(_ context.Context, accountID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumes = append(s.resumes, accountID)
	return 1, nil
}

func (s *spyRunner) RemoveTasksByAccount(context.Context, int64) (int, error) { return 0, nil }

func testBus(t *testing.T) *eventbus.Bus {
	t.Helper()
	return eventbus.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCoordinatorAuthFailedPausesTasks(t *testing.T) {
	bus := testBus(t)
	defer bus.Close(context.Background())

	spy := &spyRunner{}
	c := New(Options{Label: "test", Runner: spy})
	c.Register(bus)

	bus.Publish(context.Background(), eventbus.AccountAuthFailed{
		AccountID: 7,
		Reason:    "token expired",
		Fatal:     true,
	})
	bus.Close(context.Background())

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.pauses) != 1 {
		t.Fatalf("pauses=%d want 1", len(spy.pauses))
	}
	if spy.pauses[0].accountID != 7 || spy.pauses[0].reason != domain.PauseReasonAuthFailure {
		t.Fatalf("pause=%+v", spy.pauses[0])
	}
}

func TestCoordinatorAuthRecoveredResumesTasks(t *testing.T) {
	bus := testBus(t)
	defer bus.Close(context.Background())

	spy := &spyRunner{}
	c := New(Options{Runner: spy})
	c.Register(bus)

	bus.Publish(context.Background(), eventbus.AccountAuthRecovered{AccountID: 3})
	bus.Close(context.Background())

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.resumes) != 1 || spy.resumes[0] != 3 {
		t.Fatalf("resumes=%v", spy.resumes)
	}
}

func TestPauseByAccountRejectsInvalidReason(t *testing.T) {
	spy := &spyRunner{}
	c := New(Options{Runner: spy})
	n, err := c.PauseByAccount(context.Background(), 1, domain.PauseReasonUser, "x")
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(spy.pauses) != 0 {
		t.Fatal("user reason should not pause")
	}
}
