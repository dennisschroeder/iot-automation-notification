package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
	"github.com/dennisschroeder/iot-automation-notification/internal/logic"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/mqtt"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/nats"
	"github.com/spf13/cobra"
)

var (
	natsURL     string
	mqttBroker  string
	clientID    string
	logLevel    string
	configPath  string
	googleHomes string
	massURL     string
	piperURL    string
)

var rootCmd = &cobra.Command{
	Use:   "iot-automation-notification",
	Short: "Go Notification Engine for Homelab IoT",
	Run: func(cmd *cobra.Command, args []string) {
		setupLogger()

		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			slog.Error("Failed to load configuration", "error", err)
			os.Exit(1)
		}

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

		svc := logic.NewService(nClient, mClient, cfg, configPath, googleHomes, massURL, piperURL)
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
	rootCmd.PersistentFlags().StringVar(&clientID, "client-id", "iot-notification-service", "Unique Client ID")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "/etc/iot/config.yaml", "Path to config.yaml")
	rootCmd.PersistentFlags().StringVar(&googleHomes, "google-homes", "", "Comma separated list of Google Home media_players")
	rootCmd.PersistentFlags().StringVar(&massURL, "mass-url", "", "Music Assistant Server URL (e.g. http://mass.music-assistant:8095)")
	rootCmd.PersistentFlags().StringVar(&piperURL, "piper-url", "", "Piper TTS Service URL (e.g. piper.iot:10200)")
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
