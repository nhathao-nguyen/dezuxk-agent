package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MusicGenre thể loại âm nhạc MusicFX
type MusicGenre string

const (
	GenreElectronic MusicGenre = "ELECTRONIC"
	GenreCinematic  MusicGenre = "CINEMATIC"
	GenreAmbient    MusicGenre = "AMBIENT"
	GenreLofi       MusicGenre = "LOFI"
	GenreOrchestral MusicGenre = "ORCHESTRAL"
)

// MusicMood tâm trạng âm nhạc
type MusicMood string

const (
	MoodDramatic   MusicMood = "DRAMATIC"
	MoodEnergetic  MusicMood = "ENERGETIC"
	MoodMelancholy MusicMood = "MELANCHOLY"
	MoodUplifting  MusicMood = "UPLIFTING"
)

// FlowMusicRequest yêu cầu tạo bài nhạc nền qua MusicFX
type FlowMusicRequest struct {
	Prompt                string     `json:"prompt"`
	Genre                 MusicGenre `json:"genre,omitempty"`
	Mood                  MusicMood  `json:"mood,omitempty"`
	TempoBPM              int        `json:"tempo_bpm,omitempty"` // 60 .. 180
	SyncToVideoAssetID    string     `json:"sync_to_video_asset_id,omitempty"`
	TargetDurationSeconds int        `json:"target_duration_seconds"` // 4 .. 30
	AudioFormat           string     `json:"audio_format,omitempty"`  // WAV hoặc MP3 (mặc định WAV)
}

// FlowMusicResponse kết quả tạo nhạc MusicFX
type FlowMusicResponse struct {
	AssetID          string    `json:"asset_id"`
	URL              string    `json:"url"`
	MimeType         string    `json:"mime_type"`
	DurationSeconds  float64   `json:"duration_seconds"`
	SampleRate       int       `json:"sample_rate"`
	BeatTimestamps   []float64 `json:"beat_timestamps,omitempty"`
	CreditsDeducted  int       `json:"credits_deducted"`
	RemainingCredits int       `json:"remaining_credits"`
}

// BuildMusicFXPayload đóng gói mảng JSON cho RPC mX9w1
func BuildMusicFXPayload(projectUUID string, req FlowMusicRequest, sessionToken string, clientGuid string) []any {
	genre := req.Genre
	if genre == "" {
		genre = GenreCinematic
	}
	mood := req.Mood
	if mood == "" {
		mood = MoodDramatic
	}
	tempo := req.TempoBPM
	if tempo <= 0 {
		tempo = 120
	}
	duration := req.TargetDurationSeconds
	if duration <= 0 {
		duration = 8
	}
	format := req.AudioFormat
	if format == "" {
		format = "WAV"
	}

	configObj := map[string]any{
		"music_prompt":            req.Prompt,
		"genre":                   string(genre),
		"mood":                    string(mood),
		"tempo_bpm":               tempo,
		"target_duration_seconds": duration,
		"audio_format":            format,
		"sample_rate_hz":          44100,
		"channels":                2,
	}

	if req.SyncToVideoAssetID != "" {
		configObj["sync_to_video_asset_id"] = req.SyncToVideoAssetID
	}

	return []any{
		"projects/" + projectUUID,
		configObj,
		sessionToken,
		clientGuid,
	}
}

// ParseMusicFXResponse phân tích phản hồi RPC mX9w1
func ParseMusicFXResponse(inner string, metrics *ContractMetrics) (*FlowMusicResponse, error) {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("mX9w1", ServiceFlow, "phản hồi mX9w1 rỗng")
	}

	var objMap map[string]any
	if err := json.Unmarshal([]byte(inner), &objMap); err == nil && len(objMap) > 0 {
		res := &FlowMusicResponse{
			CreditsDeducted: 5,
		}
		if audioAsset, ok := objMap["audio_asset"].(map[string]any); ok {
			res.AssetID = fmt.Sprintf("%v", audioAsset["asset_id"])
			res.URL = fmt.Sprintf("%v", audioAsset["url"])
			res.MimeType = fmt.Sprintf("%v", audioAsset["mime_type"])
			if dur, ok := audioAsset["duration_seconds"].(float64); ok {
				res.DurationSeconds = dur
			}
			if sr, ok := audioAsset["sample_rate"].(float64); ok {
				res.SampleRate = int(sr)
			}
			if beats, ok := audioAsset["beat_timestamps"].([]any); ok {
				for _, b := range beats {
					if bf, ok := b.(float64); ok {
						res.BeatTimestamps = append(res.BeatTimestamps, bf)
					}
				}
			}
		}
		if res.URL == "" {
			ext, err := ExtractMediaDocument(inner)
			if err == nil && ext.URL != "" {
				res.URL = ext.URL
				res.MimeType = "audio/wav"
			}
		}
		return res, nil
	}

	ext, err := ExtractMediaDocument(inner)
	if err != nil {
		if metrics != nil {
			metrics.AddSchema()
		}
		return nil, CodecSchema("mX9w1", ServiceFlow, "phản hồi mX9w1 không đúng cấu trúc")
	}

	return &FlowMusicResponse{
		URL:             ext.URL,
		MimeType:        "audio/wav",
		DurationSeconds: 8.0,
		CreditsDeducted: 5,
	}, nil
}
