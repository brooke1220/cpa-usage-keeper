package test

import (
	. "cpa-usage-keeper/internal/config"
	"strings"
	"testing"
)

func TestLoadWithoutCPARunsPushOnlyReceiver(t *testing.T) {
	withIsolatedEnvFiles(t)
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CPAConfigured() || cfg.IngestMode() != "push" {
		t.Fatalf("missing CPA connection must start a push-only receiver: %+v", cfg)
	}
}

// 旧推送配置不能被静默忽略：否则中心会改为拉取，并拒收所有已部署 CPA 的上传。
func TestLoadRejectsRemovedPushSettings(t *testing.T) {
	for key, value := range map[string]string{"INGEST_MODE": "push", "INGEST_SOURCES_FILE": "/etc/keeper/ingest-sources.json"} {
		t.Run(key, func(t *testing.T) {
			withIsolatedEnvFiles(t)
			t.Setenv(key, value)
			_, err := LoadFromEnv()
			if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "Settings > Push receiving") {
				t.Fatalf("removed setting %s must fail with migration guidance, got %v", key, err)
			}
		})
	}
}

// INGEST_MODE=pull 曾是示例文件里未注释的默认行；照旧复制配置的拉取部署升级后必须能启动。
func TestLoadAcceptsLegacyPullModeLine(t *testing.T) {
	withIsolatedEnvFiles(t)
	t.Setenv("INGEST_MODE", "pull")
	t.Setenv("CPA_BASE_URL", "http://127.0.0.1:8317")
	t.Setenv("CPA_MANAGEMENT_KEY", "secret")
	cfg, err := LoadFromEnv()
	if err != nil || !cfg.CPAConfigured() {
		t.Fatalf("legacy INGEST_MODE=pull must keep pull deployments working: %+v %v", cfg, err)
	}
}

func TestLoadWithCPAKeepsPullMode(t *testing.T) {
	withIsolatedEnvFiles(t)
	t.Setenv("CPA_BASE_URL", "http://127.0.0.1:8317")
	t.Setenv("CPA_MANAGEMENT_KEY", "secret")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CPAConfigured() || cfg.IngestMode() != "pull" {
		t.Fatalf("configured CPA must keep pull mode: %+v", cfg)
	}
}
