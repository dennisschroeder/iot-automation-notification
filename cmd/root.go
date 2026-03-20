package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/dennisschroeder/iot-automation-template-go/internal/logic"
	"github.com/dennisschroeder/iot-automation-template-go/internal/transport/mqtt"
	"github.com/dennisschroeder/iot-automation-template-go/internal/transport/nats"
	"github.com/spf13/cobra"
)

var (
	natsURL    string
	mqttBroker string
	clientID   string
	logLevel   string
)

var rootCmd = &cobra.Command{
	Use:   "iot-automation",
	Short: "Go Automation Engine for Homelab IoT",
	Run: func(cmd *cobra.Command, args []string) {
		setupLogger()

		nClient, err := nats.NewClient(natsURL)
		if err != nil {
			slog.Error("Failed to initialize NATS", "error", err)
			os.Exit(1)
		}
		defer nClient.Close()

		mClient, err := mqtt.NewClient(mqttBroker, clientID)
		if err != nil {
			slog.Error("Failed to initialize MQTT", "error", err)
			os.Exit(1)
		}
		defer mClient.Close()

		svc := logic.NewService(nClient, mClient)
		if err := svc.Run(context.Background()); err != nil {
			slog.Error("Service execution failed", "error", err)
			os.Exit(1)
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		slog.Error("Execution failed", "error", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&natsURL, "nats-url", "nats://nats.event-bus:4222", "NATS Server URL")
	rootCmd.PersistentFlags().StringVar(&mqttBroker, "mqtt-broker", "tcp://mosquitto.iot:1883", "MQTT Broker URL")
	rootCmd.PersistentFlags().StringVar(&clientID, "client-id", "iot-automation", "Unique Client ID")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
}

func setupLogger() {
	var level slog.Level
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
}
