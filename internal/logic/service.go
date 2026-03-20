package logic

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dennisschroeder/iot-automation-template-go/internal/transport/mqtt"
	"github.com/dennisschroeder/iot-automation-template-go/internal/transport/nats"
	natsgo "github.com/nats-io/nats.go"
)

type Service struct {
	nats *nats.Client
	mqtt *mqtt.Client
}

func NewService(n *nats.Client, m *mqtt.Client) *Service {
	return &Service{
		nats: n,
		mqtt: m,
	}
}

func (s *Service) Run(ctx context.Context) error {
	slog.Info("Starting IoT Automation logic...")

	// Example Watcher: Listen to JetStream events
	_, err := s.nats.Subscribe("iot.events.>", func(msg *natsgo.Msg) {
		slog.Debug("Received event", "subject", msg.Subject, "data", string(msg.Data))
		// Implement your logic here
	})
	if err != nil {
		return err
	}

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-ctx.Done():
		slog.Info("Context cancelled, shutting down")
	case sig := <-stop:
		slog.Info("Signal received, shutting down", "signal", sig)
	}

	return nil
}
