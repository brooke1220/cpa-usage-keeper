package config

// CPAConfigured 表示 Keeper 连接了一台 CPA，会照常拉取用量和管理数据。
// 没有配置 CPA 时 Keeper 只接收推送，推送来源和开关都在设置页管理。
func (cfg Config) CPAConfigured() bool { return cfg.CPABaseURL != "" && cfg.CPAManagementKey != "" }

// IngestMode 是返回给页面的模式：pull 表示连接了 CPA（可同时接收推送），push 表示只接收推送。
func (cfg Config) IngestMode() string {
	if cfg.CPAConfigured() {
		return "pull"
	}
	return "push"
}
