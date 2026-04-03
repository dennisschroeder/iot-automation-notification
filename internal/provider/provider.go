package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
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

// GoogleHomeProvider stub for TTS notifications
type GoogleHomeProvider struct{}

func (g *GoogleHomeProvider) Name() string { return "google_home" }
func (g *GoogleHomeProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("Google Home TTS (Stub)", "target", act.Target, "message", act.Message)
	return nil
}

// TVProvider stub for Screen notifications
type TVProvider struct{}

func (t *TVProvider) Name() string { return "tv" }
func (t *TVProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("TV Toast Notification (Stub)", "target", act.Target, "message", act.Message)
	return nil
}
