package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPayload(t *testing.T) {
	audioURL := "http://test.com/audio.wav"
	actTarget := "test_player"
	
	params := map[string]interface{}{
		"player_id": actTarget,
		"announcement": map[string]interface{}{
			"uri":        audioURL,
			"media_type": "announcement",
		},
		"volume_level": 50,
	}

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"command": "players/play_announcement",
		"args":    params,
	}
	
	jsonPayload, _ := json.Marshal(payload)
	fmt.Println(string(jsonPayload))
}
