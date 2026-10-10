package logic

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
	"github.com/dennisschroeder/iot-automation-notification/internal/provider"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/binary_sensor"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/common"
)

type emptyKV struct{}

func (emptyKV) GetKV(string) (string, error) { return "", errors.New("not found") }

type slowProvider struct {
	delay time.Duration
	calls atomic.Int32
}

func (p *slowProvider) Name() string { return "slow" }

func (p *slowProvider) Send(ctx context.Context, _ config.Action) error {
	p.calls.Add(1)
	time.Sleep(p.delay)
	return nil
}

func newTestService(p *slowProvider, debounce time.Duration) *Service {
	return &Service{
		kv:        emptyKV{},
		providers: map[string]provider.NotificationProvider{p.Name(): p},
		lastFired: make(map[string]time.Time),
		cfg: &config.Config{Notifications: []config.NotificationRule{{
			ID:       "doorbell",
			Debounce: debounce,
			Trigger: config.Trigger{
				Devices: []string{"me_doorbell"},
				Type:    "binary_sensor",
				State:   "ON",
			},
			Actions: []config.Action{{Channel: "slow"}},
		}}},
	}
}

var doorbellOn = &binary_sensor.BinarySensorEvent{
	EntityId: "binary_sensor.me_doorbell",
	State:    common.BinaryState_BINARY_STATE_ON,
}

func TestDoorbellBurstWithSlowProviderFiresOnce(t *testing.T) {
	p := &slowProvider{delay: 300 * time.Millisecond}
	s := newTestService(p, config.DefaultDebounce)

	start := time.Now()
	for i := 0; i < 4; i++ {
		s.handleBinarySensorEvent(context.Background(), doorbellOn)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("event handling blocked on provider for %v", elapsed)
	}

	if !s.waitInflight(time.Second) {
		t.Fatal("executions did not finish")
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 execution, got %d", got)
	}
}

func TestDebounceWindowExpires(t *testing.T) {
	p := &slowProvider{}
	s := newTestService(p, 50*time.Millisecond)

	s.handleBinarySensorEvent(context.Background(), doorbellOn)
	s.handleBinarySensorEvent(context.Background(), doorbellOn)
	time.Sleep(80 * time.Millisecond) // let the 50ms window expire
	s.handleBinarySensorEvent(context.Background(), doorbellOn)
	if !s.waitInflight(time.Second) {
		t.Fatal("executions did not finish")
	}

	if got := p.calls.Load(); got != 2 {
		t.Fatalf("expected 2 executions (one per window), got %d", got)
	}
}

func TestShutdownRejectsNewExecutionsAndWaitsForInflight(t *testing.T) {
	p := &slowProvider{delay: 100 * time.Millisecond}
	s := newTestService(p, config.DefaultDebounce)

	s.handleBinarySensorEvent(context.Background(), doorbellOn)
	if !s.shutdown(time.Second) {
		t.Fatal("shutdown timed out")
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("expected in-flight execution to complete, got %d calls", got)
	}

	s.lastFired = make(map[string]time.Time)
	s.handleBinarySensorEvent(context.Background(), doorbellOn)
	if !s.waitInflight(time.Second) {
		t.Fatal("unexpected in-flight execution")
	}
	if got := p.calls.Load(); got != 1 {
		t.Fatalf("expected no execution after shutdown, got %d calls", got)
	}
}
