package test

import (
	. "cpa-usage-keeper/internal/config"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPushModeNeedsNoCPAConnection(t *testing.T) {
	withIsolatedEnvFiles(t)
	hash := sha256.Sum256([]byte(strings.Repeat("s", 32)))
	path := filepath.Join(t.TempDir(), "sources.json")
	raw, _ := json.Marshal([]PushSource{{SourceID: "cpa-one", TokenSHA256: hex.EncodeToString(hash[:])}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INGEST_MODE", "push")
	t.Setenv("INGEST_SOURCES_FILE", path)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PushMode() || cfg.CPABaseURL != "" || cfg.CPAManagementKey != "" || len(cfg.IngestSources) != 1 {
		t.Fatalf("push config incorrect: %+v", cfg)
	}
	t.Setenv("INGEST_SOURCES_FILE", "")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("push mode accepted no authenticated sources")
	}
}

func TestPushSourcesRejectAmbiguousCredentials(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	for _, sources := range [][]PushSource{
		nil, {{SourceID: "bad source", TokenSHA256: hash}}, {{SourceID: "one", TokenSHA256: "invalid"}},
		{{SourceID: "one", TokenSHA256: hash}, {SourceID: "two", TokenSHA256: hash}},
		{{SourceID: "one", TokenSHA256: hash}, {SourceID: "one", TokenSHA256: strings.Repeat("cd", 32)}},
	} {
		if err := ValidatePushSources(sources); err == nil {
			t.Fatal("ambiguous source configuration accepted")
		}
	}
}
