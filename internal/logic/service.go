package logic

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
	"github.com/dennisschroeder/iot-automation-notification/internal/provider"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/mqtt"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/nats"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/binary_sensor"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/common"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/envelope"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/light"
	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

type Service struct {
	nats       *nats.Client
	mqtt       *mqtt.Client
	cfg        *config.Config
	configPath string
	activeIDs  map[string]bool
	providers  map[string]provider.NotificationProvider
	lastFired  map[string]time.Time
	mu         sync.Mutex
}

func NewService(n *nats.Client, m *mqtt.Client, cfg *config.Config, configPath string) *Service {
	s := &Service{
		nats:       n,
		mqtt:       m,
		cfg:        cfg,
		configPath: configPath,
		activeIDs:  make(map[string]bool),
		providers:  make(map[string]provider.NotificationProvider),
		lastFired:  make(map[string]time.Time),
	}

	// Register providers
	ha := provider.NewHomeAssistantProvider(n)
	s.providers[ha.Name()] = ha
	gh := &provider.GoogleHomeProvider{}
	s.providers[gh.Name()] = gh
	tv := &provider.TVProvider{}
	s.providers[tv.Name()] = tv

	return s
}

func (s *Service) Run(ctx context.Context) error {
	slog.Info("Starting IoT Notification Service...", "notifications_count", len(s.cfg.Notifications))

	// Discovery and State management (similar to presence-automation)
	s.setupMuteSwitches()

	// Subscribe to ALL v1 events
	subject := "iot.v1.events.>"
	_, err := s.nats.Subscribe(subject, func(msg *natsgo.Msg) {
		var env envelope.EventEnvelope
		if err := proto.Unmarshal(msg.Data, &env); err != nil {
			return
		}

		if bs := env.GetBinarySensor(); bs != nil {
			slog.Info("Received binary sensor event", "entity_id", bs.EntityId, "state", bs.State, "class", bs.DeviceClass)
			if strings.Contains(bs.EntityId, "doorbell") {
				slog.Info("DOORBELL EVENT DETECTED", "entity_id", bs.EntityId, "state", bs.State)
			}
			s.handleBinarySensorEvent(ctx, bs)
		} else if light := env.GetLight(); light != nil {
			slog.Info("Received light event", "entity_id", light.EntityId, "state", light.State)
			s.handleLightEvent(ctx, light)
		}
	})
	if err != nil {
		return fmt.Errorf("nats subscribe failed: %w", err)
	}

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

func (s *Service) setupMuteSwitches() {
	for _, rule := range s.cfg.Notifications {
		s.activeIDs[rule.ID] = true
		swID := rule.ID
		discoveryTopic := fmt.Sprintf("notification_%s", swID)
		friendlyName := "Mute: " + strings.ReplaceAll(swID, "_", " ")

		discoveryPayload := map[string]interface{}{
			"name":          friendlyName,
			"object_id":     discoveryTopic,
			"unique_id":     "iot_notification_" + swID,
			"state_topic":   fmt.Sprintf("iot-automation-notification/switch/%s/state", swID),
			"command_topic": fmt.Sprintf("iot-automation-notification/switch/%s/set", swID),
			"payload_on":    "ON",
			"payload_off":   "OFF",
			"device": map[string]interface{}{
				"identifiers":  []string{"iot_notification_engine_device"},
				"name":         "IoT Notification Engine",
				"manufacturer": "Custom",
				"model":        "Go Notification Engine",
			},
		}
		s.mqtt.PublishDiscovery("switch", discoveryTopic, discoveryPayload)

		kvKey := fmt.Sprintf("lock.%s", swID)
		valStr, _ := s.nats.GetKV(kvKey)
		state := "ON" // Default to unmuted (lock=false)
		if valStr == "true" {
			state = "OFF" // Locked/Muted
		} else if valStr == "" {
			s.nats.PutKV(kvKey, []byte("false"))
		}
		s.mqtt.Publish(fmt.Sprintf("iot-automation-notification/switch/%s/state", swID), state)
	}

	// MQTT command listener
	s.mqtt.Subscribe("iot-automation-notification/switch/+/set", func(topic string, payload []byte) {
		parts := strings.Split(topic, "/")
		if len(parts) != 4 {
			return
		}
		swID := parts[2]
		state := string(payload)
		kvVal := "false"
		if state == "OFF" {
			kvVal = "true"
		}
		s.nats.PutKV(fmt.Sprintf("lock.%s", swID), []byte(kvVal))
		s.mqtt.Publish(fmt.Sprintf("iot-automation-notification/switch/%s/state", swID), state)
	})
}

func (s *Service) handleBinarySensorEvent(ctx context.Context, bs *binary_sensor.BinarySensorEvent) {
	stateStr := "OFF"
	if bs.State == common.BinaryState_BINARY_STATE_ON {
		stateStr = "ON"
	}

	for _, rule := range s.cfg.Notifications {
		match := false
		for _, dName := range rule.Trigger.Devices {
			slog.Info("Checking rule trigger", "rule", rule.ID, "target_device", dName, "event_device", bs.EntityId, "event_state", stateStr, "expected_state", rule.Trigger.State)
			if (bs.EntityId == dName || strings.HasSuffix(bs.EntityId, "."+dName)) && 
				rule.Trigger.Type == "binary_sensor" && rule.Trigger.State == stateStr {
				match = true
				break
			}
		}

		if match {
			slog.Info("Rule matched, executing", "rule", rule.ID)
			s.evaluateAndExecute(ctx, rule)
		}
	}
}

func (s *Service) handleLightEvent(ctx context.Context, l *light.LightEvent) {
	stateStr := "OFF"
	if l.State == common.BinaryState_BINARY_STATE_ON {
		stateStr = "ON"
	}

	for _, rule := range s.cfg.Notifications {
		match := false
		for _, dName := range rule.Trigger.Devices {
			if (l.EntityId == dName || strings.HasSuffix(l.EntityId, "."+dName)) && 
				rule.Trigger.Type == "light" && rule.Trigger.State == stateStr {
				match = true
				break
			}
		}

		if match {
			s.evaluateAndExecute(ctx, rule)
		}
	}
}

func (s *Service) evaluateAndExecute(ctx context.Context, rule config.NotificationRule) {
	// 0. Debounce Check
	s.mu.Lock()
	if last, ok := s.lastFired[rule.ID]; ok {
		if time.Since(last) < 2*time.Second {
			slog.Debug("Debouncing rule execution", "rule", rule.ID)
			s.mu.Unlock()
			return
		}
	}
	s.lastFired[rule.ID] = time.Now()
	s.mu.Unlock()

	// 1. Mute/Lock Check
	lockKey := fmt.Sprintf("lock.%s", rule.ID)
	lockVal, _ := s.nats.GetKV(lockKey)
	if lockVal == "true" {
		return
	}

	// 2. Conditions Check
	for _, cond := range rule.Conditions {
		if !s.checkCondition(cond) {
			return
		}
	}

	// 3. Execute Actions
	for _, act := range rule.Actions {
		if p, ok := s.providers[act.Channel]; ok {
			if err := p.Send(ctx, act); err != nil {
				slog.Error("Failed to send notification", "rule", rule.ID, "channel", act.Channel, "error", err)
			}
		} else {
			slog.Warn("Unknown notification channel", "channel", act.Channel)
		}
	}
}

func (s *Service) checkCondition(cond config.Condition) bool {
	// Stub for time condition
	if cond.Type == "time" {
		return true // Implement actual time logic if needed
	}

	valStr, err := s.nats.GetKV(cond.Device)
	if err != nil {
		if cond.Default != nil {
			valStr = fmt.Sprintf("%v", cond.Default)
		} else {
			return false
		}
	}

	targetStr := fmt.Sprintf("%v", cond.Value)

	if cond.Operator == "==" {
		return strings.EqualFold(valStr, targetStr)
	} else if cond.Operator == "!=" {
		return !strings.EqualFold(valStr, targetStr)
	} else if cond.Operator == "<" || cond.Operator == ">" || cond.Operator == "<=" || cond.Operator == ">=" {
		valFloat, err1 := strconv.ParseFloat(valStr, 64)
		targetFloat, err2 := strconv.ParseFloat(targetStr, 64)
		if err1 != nil || err2 != nil {
			return false
		}
		switch cond.Operator {
		case "<": return valFloat < targetFloat
		case ">": return valFloat > targetFloat
		case "<=": return valFloat <= targetFloat
		case ">=": return valFloat >= targetFloat
		}
	}
	return false
}
