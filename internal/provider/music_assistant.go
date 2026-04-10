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
			
			// Create a context with timeout for synthesis
			synthCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			
			audioData, err := m.synthesizeSpeech(synthCtx, act.Message)
			if err != nil {
				slog.Warn("Piper synthesis failed, falling back to MAS internal TTS", "error", err)
			} else if len(audioData) > 0 {
				slog.Info("Successfully generated audio data", "bytes", len(audioData))
				if err := os.WriteFile(filePath, audioData, 0644); err != nil {
					slog.Error("Failed to save audio to cache", "error", err)
				} else {
					audioURL = fmt.Sprintf("%s/cache/%s", m.callbackURL, filename)
					slog.Info("Audio saved to cache", "url", audioURL)
				}
			} else {
				slog.Warn("Piper returned empty audio data")
			}
		} else {
			slog.Info("Using cached TTS audio", "filename", filename)
			audioURL = fmt.Sprintf("%s/cache/%s", m.callbackURL, filename)
		}
	}

	// 2. Prepare JSON-RPC payload for Music Assistant 2.0+
	params := map[string]interface{}{
		"player_id": act.Target,
		"announcement": map[string]interface{}{
			"uri":        audioURL,
			"media_type": "announcement",
		},
		"volume_level": 50,
	}

	if audioURL == "" {
		params["announcement"].(map[string]interface{})["uri"] = "tts://" + act.Message
	}

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"command": "players/play_announcement",
		"args":    params,
	}

	jsonPayload, _ := json.Marshal(payload)
	slog.Debug("Sending command to Music Assistant API", "payload", string(jsonPayload))
	
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api", m.massURL), bytes.NewBuffer(jsonPayload))
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

	// Log response for debugging player IDs
	body, _ := io.ReadAll(resp.Body)
	slog.Info("Music Assistant response", "body", string(body))

	return nil
}

// synthesizeSpeech is a placeholder for the Wyoming protocol implementation.
// For the first iteration, we rely on MAS's internal TTS provider configuration
// as discussed, but keep this hook for future local Piper direct streaming.
func (m *MusicAssistantProvider) synthesizeSpeech(ctx context.Context, text string) ([]byte, error) {
	if m.piperURL == "" {
		return nil, nil
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", m.piperURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to piper: %w", err)
	}
	defer conn.Close()

	// 1. Send Synthesize Event
	synthesizeEvent := fmt.Sprintf(`{"type": "synthesize", "data": {"text": "%s"}}`+"\n", text)
	slog.Debug("Sending synthesize event to Piper", "event", synthesizeEvent)
	if _, err := conn.Write([]byte(synthesizeEvent)); err != nil {
		return nil, fmt.Errorf("failed to send synthesize event: %w", err)
	}

	var rawBuffer bytes.Buffer
	reader := bufio.NewReader(conn)
	sampleRate := 22050 // Default for Piper Kerstin

	// 2. Read Response Events
	for {
		// Read JSON header line
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read wyoming header: %w", err)
		}

		// Robustness: Skip any binary garbage before the JSON header
		if !bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			idx := bytes.Index(line, []byte("{"))
			if idx == -1 {
				slog.Debug("Skipping non-JSON line from Piper", "len", len(line))
				continue
			}
			line = line[idx:]
		}

		var event struct {
			Type          string `json:"type"`
			PayloadLength *int   `json:"payload_length"`
			DataLength    *int   `json:"data_length"`
			Data          struct {
				Rate int `json:"rate"`
			} `json:"data"`
		}

		if err := json.Unmarshal(line, &event); err != nil {
			// This might be binary data if we lost sync
			slog.Warn("Failed to unmarshal Wyoming header", "line_len", len(line), "error", err)
			continue
		}

		if event.Data.Rate > 0 {
			sampleRate = event.Data.Rate
		}

		// 3. Read binary payload if present
		// Check both payload_length and data_length as some Wyoming versions use different names
		length := 0
		if event.PayloadLength != nil {
			length = *event.PayloadLength
		} else if event.DataLength != nil {
			length = *event.DataLength
		}

		if length > 0 {
			payload := make([]byte, length)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return nil, fmt.Errorf("failed to read binary payload: %w", err)
			}

			if event.Type == "audio-chunk" {
				rawBuffer.Write(payload)
			}
		}

		if event.Type == "audio-stop" {
			slog.Debug("Received audio-stop from Piper")
			break
		}
	}

	if rawBuffer.Len() == 0 {
		return nil, nil
	}

	// 4. Add WAV Header
	return addWavHeader(rawBuffer.Bytes(), sampleRate), nil
}

func addWavHeader(pcmData []byte, sampleRate int) []byte {
	bitsPerSample := 16
	channels := 1
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcmData)
	chunkSize := 36 + dataSize

	header := new(bytes.Buffer)
	header.Write([]byte("RIFF"))
	header.Write(uint32ToBytes(uint32(chunkSize)))
	header.Write([]byte("WAVE"))
	header.Write([]byte("fmt "))
	header.Write(uint32ToBytes(uint32(16))) // Subchunk1Size
	header.Write(uint16ToBytes(uint16(1)))  // AudioFormat (PCM)
	header.Write(uint16ToBytes(uint16(channels)))
	header.Write(uint32ToBytes(uint32(sampleRate)))
	header.Write(uint32ToBytes(uint32(byteRate)))
	header.Write(uint16ToBytes(uint16(blockAlign)))
	header.Write(uint16ToBytes(uint16(bitsPerSample)))
	header.Write([]byte("data"))
	header.Write(uint32ToBytes(uint32(dataSize)))
	header.Write(pcmData)

	return header.Bytes()
}

func uint32ToBytes(v uint32) []byte {
	b := make([]byte, 4)
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	return b
}

func uint16ToBytes(v uint16) []byte {
	b := make([]byte, 2)
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	return b
}
