package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/mqtt"
	"github.com/dennisschroeder/iot-automation-notification/internal/transport/nats"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/action"
	"github.com/dennisschroeder/iot-schemas-proto/proto/v1/notification"
	"google.golang.org/protobuf/proto"
)

type NotificationProvider interface {
	Send(ctx context.Context, action config.Action) error
	Name() string
}

// HomeAssistantProvider sends notifications via Home Assistant's notify service
// It uses the NATS event bus to request the action, which is picked up by a bridge
type HomeAssistantProvider struct {
	nats *nats.Client
}

func NewHomeAssistantProvider(n *nats.Client) *HomeAssistantProvider {
	return &HomeAssistantProvider{nats: n}
}

func (h *HomeAssistantProvider) Name() string { return "mobile_app" }

func (h *HomeAssistantProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("Sending HA Notification", "target", act.Target, "title", act.Title)

	// We use the 'notify' action type if available in schemas, 
	// or we genericize it as a service call.
	// For now, we'll follow the pattern of publishing to a specific NATS subject.
	
	// Implementation note: The actual HA service call (notify.mobile_app_<target>)
	// is handled by the bridge that listens to this NATS topic.
	
	var dataMap map[string]string
	if len(act.Data) > 0 {
		dataMap = make(map[string]string)
		for k, v := range act.Data {
			dataMap[k] = fmt.Sprintf("%v", v)
		}
	}

	payload := &action.ActionRequest{
		Id:           fmt.Sprintf("notif_%d", time.Now().UnixNano()),
		TargetEntity: fmt.Sprintf("notify.mobile_app_%s", act.Target),
		Command: &action.ActionRequest_Notification{
			Notification: &notification.NotificationCommand{
				Title:   act.Title,
				Message: act.Message,
				Data:    dataMap,
			},
		},
	}

	data, err := proto.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	subject := fmt.Sprintf("iot.v1.actions.notification.%s", act.Target)
	return h.nats.Publish(subject, data)
}

// GoogleHomeProvider implementation for TTS notifications with volume control
type GoogleHomeProvider struct {
	mqttClient *mqtt.Client
	targets    []string
	volumes    map[string]float64
	mu         sync.RWMutex
}

func NewGoogleHomeProvider(m *mqtt.Client, targets string) *GoogleHomeProvider {
	gh := &GoogleHomeProvider{
		mqttClient: m,
		volumes:    make(map[string]float64),
	}
	
	if targets != "" {
		parts := strings.Split(targets, ",")
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				gh.targets = append(gh.targets, t)
			}
		}
	}

	// Subscribe to volume updates from HA statestream
	m.Subscribe("homeassistant/media_player/+/volume_level", func(topic string, payload []byte) {
		parts := strings.Split(topic, "/")
		if len(parts) == 4 {
			entityID := "media_player." + parts[2]
			vol, err := strconv.ParseFloat(string(payload), 64)
			if err == nil {
				gh.mu.Lock()
				gh.volumes[entityID] = vol
				gh.mu.Unlock()
			}
		}
	})

	return gh
}

func (g *GoogleHomeProvider) Name() string { return "google_home" }

func (g *GoogleHomeProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("Google Home TTS", "target", act.Target, "message", act.Message)

	for _, target := range g.targets {
		g.mu.RLock()
		vol := g.volumes[target]
		g.mu.RUnlock()

		if vol < 0.6 {
			slog.Info("Increasing Google Home volume", "target", target, "current_vol", vol)
			volPayload, _ := json.Marshal(map[string]interface{}{
				"entity_id": target,
				"data": map[string]interface{}{
					"volume_level": 0.6,
				},
			})
			g.mqttClient.Publish("homeassistant/service/media_player/volume_set", volPayload)
			time.Sleep(500 * time.Millisecond) // Give HA some time to apply volume
		}

		ttsPayload, _ := json.Marshal(map[string]interface{}{
			"entity_id": target,
			"data": map[string]interface{}{
				"message": act.Message,
			},
		})
		g.mqttClient.Publish("homeassistant/service/tts/google_translate_say", ttsPayload)
	}

	return nil
}

// TVProvider stub for Screen notifications
type TVProvider struct{}

func (t *TVProvider) Name() string { return "tv" }
func (t *TVProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("TV Toast Notification (Stub)", "target", act.Target, "message", act.Message)
	return nil
}
