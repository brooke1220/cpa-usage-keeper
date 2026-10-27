package config

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

type PushSource struct {
	SourceID    string `json:"source_id"`
	TokenSHA256 string `json:"token_sha256"`
}

func (cfg Config) PushMode() bool { return cfg.IngestMode == "push" }

func LoadPushSources(path string) ([]PushSource, error) {
	if path == "" {
		return nil, fmt.Errorf("INGEST_SOURCES_FILE is required in push mode")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ingest sources: %w", err)
	}
	var sources []PushSource
	if json.Unmarshal(raw, &sources) != nil {
		return nil, fmt.Errorf("ingest sources must be a JSON array")
	}
	if err := ValidatePushSources(sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func ValidatePushSources(sources []PushSource) error {
	if len(sources) == 0 {
		return fmt.Errorf("at least one push source is required")
	}
	ids, tokens := map[string]bool{}, map[string]bool{}
	pattern := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
	for _, s := range sources {
		hash, err := hex.DecodeString(s.TokenSHA256)
		if !pattern.MatchString(s.SourceID) || err != nil || len(hash) != 32 {
			return fmt.Errorf("invalid ingest source ID or token SHA-256")
		}
		canonical := hex.EncodeToString(hash)
		if ids[s.SourceID] || tokens[canonical] {
			return fmt.Errorf("ingest source IDs and token hashes must be unique")
		}
		ids[s.SourceID], tokens[canonical] = true, true
	}
	return nil
}
