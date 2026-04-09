package provider

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/dennisschroeder/iot-automation-notification/internal/config"
)

// MusicAssistantProvider sends announcements via Music Assistant JSON-RPC API.
// It uses a local Piper service to generate the TTS audio.
type MusicAssistantProvider struct {
	massURL     string
	piperURL    string
	cacheDir    string
	callbackURL string
}

func NewMusicAssistantProvider(massURL, piperURL string, cacheDir string, callbackURL string) *MusicAssistantProvider {
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			slog.Error("Failed to create cache directory", "path", cacheDir, "error", err)
		}
	}

	return &MusicAssistantProvider{
		massURL:     massURL,
		piperURL:    piperURL,
		cacheDir:    cacheDir,
		callbackURL: callbackURL,
	}
}

func (m *MusicAssistantProvider) Name() string { return "music_assistant" }

func (m *MusicAssistantProvider) Send(ctx context.Context, act config.Action) error {
	slog.Info("Sending Music Assistant Announcement", "target", act.Target, "message", act.Message)

	var audioURL string

	// 1. Check Cache and Generate Audio via Piper if needed
	if m.cacheDir != "" && m.callbackURL != "" {
		hash := sha256.Sum256([]byte(act.Message))
		filename := hex.EncodeToString(hash[:]) + ".wav"
		filePath := filepath.Join(m.cacheDir, filename)

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			slog.Info("Generating new TTS audio", "message", act.Message)
			audioData, err := m.synthesizeSpeech(act.Message)
			if err != nil {
				slog.Warn("Piper synthesis failed, falling back to MAS internal TTS", "error", err)
			} else if len(audioData) > 0 {
				if err := os.WriteFile(filePath, audioData, 0644); err != nil {
					slog.Error("Failed to save audio to cache", "error", err)
				} else {
					audioURL = fmt.Sprintf("%s/cache/%s", m.callbackURL, filename)
				}
			}
		} else {
			slog.Info("Using cached TTS audio", "filename", filename)
			audioURL = fmt.Sprintf("%s/cache/%s", m.callbackURL, filename)
		}
	}

	// 2. Prepare JSON-RPC payload
	params := map[string]interface{}{
		"player_id":     act.Target,
		"use_streaming": true,
	}

	if audioURL != "" {
		params["url"] = audioURL
	} else {
		params["message"] = act.Message
	}

	payload := map[string]interface{}{
		"id":      1,
		"jsonrpc": "2.0",
		"method":  "players/play_announcement",
		"params":  params,
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
	reader := bufio.NewReader(conn)

	// 2. Read Response Events
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read wyoming event: %w", err)
		}

		var event struct {
			Type          string `json:"type"`
			PayloadLength int    `json:"payload_length"`
		}

		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		// Read binary payload if present
		if event.PayloadLength > 0 {
			payload := make([]byte, event.PayloadLength)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return nil, fmt.Errorf("failed to read payload: %w", err)
			}

			if event.Type == "audio-chunk" {
				audioBuffer.Write(payload)
			}
		}

		if event.Type == "audio-stop" {
			break
		}
	}

	return audioBuffer.Bytes(), nil
}
