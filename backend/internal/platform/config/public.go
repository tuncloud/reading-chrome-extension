package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed default_config.json
var defaultPublicJSON []byte

// Tracking holds the 🔶 thresholds shared by extension and worker (spec §2.10, §7).
type Tracking struct {
	IdleSeconds           int     `json:"idle_seconds"`
	SegmentSeconds        int     `json:"segment_seconds"`
	MinCreateSeconds      int     `json:"min_create_seconds"`
	CompleteProgress      float64 `json:"complete_progress"`
	CompleteTimeRatio     float64 `json:"complete_time_ratio"`
	WordsPerMinute        int     `json:"words_per_minute"`
	SyncIntervalMinutes   int     `json:"sync_interval_minutes"`
	SyncThresholdSegments int     `json:"sync_threshold_segments"`
}

// PublicConfig is the GET /v1/config body.
type PublicConfig struct {
	Version          int      `json:"version"`
	Tracking         Tracking `json:"tracking"`
	FreediumHosts    []string `json:"freedium_hosts"`
	StripQueryParams []string `json:"strip_query_params"`
}

// LoadPublic returns the embedded defaults with TRACKING_CONFIG_JSON merged on top.
// Unknown keys are rejected so a typo can't silently fall back to a default.
func LoadPublic(override string) (PublicConfig, error) {
	var pc PublicConfig
	if err := json.Unmarshal(defaultPublicJSON, &pc); err != nil {
		return pc, fmt.Errorf("config: embedded defaults: %w", err)
	}
	if strings.TrimSpace(override) != "" {
		dec := json.NewDecoder(strings.NewReader(override))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&pc); err != nil {
			return pc, fmt.Errorf("config: TRACKING_CONFIG_JSON: %w", err)
		}
	}
	return pc, pc.validate()
}

func (pc PublicConfig) validate() error {
	t := pc.Tracking
	if t.IdleSeconds < 1 || t.SegmentSeconds < 1 || t.MinCreateSeconds < 0 || t.WordsPerMinute < 1 ||
		t.SyncIntervalMinutes < 1 || t.SyncThresholdSegments < 1 {
		return fmt.Errorf("config: tracking values must be positive: %+v", t)
	}
	if t.CompleteProgress <= 0 || t.CompleteProgress > 1 || t.CompleteTimeRatio < 0 {
		return fmt.Errorf("config: completion thresholds out of range: %+v", t)
	}
	return nil
}
