package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
)

// MusicAssistantProvider sends announcements via Music Assistant JSON-RPC API.
// It uses a local Piper service to generate the TTS audio.
type MusicAssistantProvider struct {
	massURL  string
	piperURL string
}

func NewMusicAssistantProvider(massURL, piperURL string) *MusicAssistantProvider {
	return &MusicAssistantProvider{
		massURL:  massURL,
		piperURL: piperURL,
	}
}

func (m *MusicAssistantProvider) Name() string { return "music_assistant" }

func (m *MusicAssistantProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("Sending Music Assistant Announcement", "target", act.Target, "message", act.Message)

	// 1. Generate Audio via Piper (Wyoming Protocol)
	audioData, err := m.synthesizeSpeech(act.Message)
	if err != nil {
		slog.Warn("Piper synthesis failed, falling back to MAS internal TTS", "error", err)
	}

	// 2. Prepare JSON-RPC payload
	payload := map[string]interface{}{
		"id":      1,
		"jsonrpc": "2.0",
		"method":  "players/play_announcement",
		"params": map[string]interface{}{
			"player_id":     act.Target,
			"use_streaming": true,
		},
	}

	// If we have audio data, we'll need to host it or pass it.
	// For this iteration, we use the message parameter for MAS internal TTS.
	// If audioData is available, we could potentially use it in the future
	// when we have an internal webserver to host the buffer.
	if len(audioData) > 0 {
		slog.Info("Audio data generated via Piper", "bytes", len(audioData))
		// Note: We currently don't have a way to pass raw bytes to MAS via JSON-RPC.
		// We would need a sidecar or internal webserver to serve this audioData.
	}

	params := payload["params"].(map[string]interface{})
	params["message"] = act.Message

	// If a URL is provided in Data, use it instead of message
	if url, ok := act.Data["url"].(string); ok {
		payload["params"].(map[string]interface{})["url"] = url
		delete(payload["params"].(map[string]interface{}), "message")
	}

	jsonPayload, _ := json.Marshal(payload)
	
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/json-rpc", m.massURL), bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call music assistant api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("music assistant api returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// synthesizeSpeech is a placeholder for the Wyoming protocol implementation.
// For the first iteration, we rely on MAS's internal TTS provider configuration
// as discussed, but keep this hook for future local Piper direct streaming.
func (m *MusicAssistantProvider) synthesizeSpeech(text string) ([]byte, error) {
	if m.piperURL == "" {
		return nil, nil
	}

	conn, err := net.DialTimeout("tcp", m.piperURL, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to piper: %w", err)
	}
	defer conn.Close()

	// 1. Send Synthesize Event
	// Wyoming Event: JSON\n[Binary]
	synthesizeEvent := fmt.Sprintf(`{"type": "synthesize", "data": {"text": "%s"}}`+"\n", text)
	if _, err := conn.Write([]byte(synthesizeEvent)); err != nil {
		return nil, fmt.Errorf("failed to send synthesize event: %w", err)
	}

	var audioBuffer bytes.Buffer
	decoder := json.NewDecoder(conn)

	// 2. Read Response Events
	for {
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}

		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to decode wyoming event: %w", err)
		}

		if event.Type == "audio-chunk" {
			var chunk struct {
				Data string `json:"data"` // Wyoming sometimes sends data in the header or as trailing binary
			}
			// In Wyoming, if the event has a data payload, it follows the JSON header.
			// But since we are using a decoder, we need to be careful with the binary part.
			// For now, we log the chunk receipt.
			slog.Debug("Received audio chunk from Piper")
		}

		if event.Type == "audio-stop" {
			break
		}
	}

	return audioBuffer.Bytes(), nil
}
