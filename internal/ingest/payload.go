package ingest

import (
	"encoding/json"
	"time"
)

type tokenStats struct {
	Input            int64 `json:"input_tokens"`
	Output           int64 `json:"output_tokens"`
	Reasoning        int64 `json:"reasoning_tokens"`
	Cached           int64 `json:"cached_tokens"`
	CacheRead        int64 `json:"cache_read_tokens"`
	CacheReadPresent bool  `json:"cache_read_tokens_present"`
	CacheCreation    int64 `json:"cache_creation_tokens"`
	Total            int64 `json:"total_tokens"`
}

// usagePayload is a whitelist: no response bodies, arbitrary headers or upstream secrets.
type usagePayload struct {
	Timestamp            time.Time `json:"timestamp"`
	APIKey               string    `json:"api_key"`
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	Alias                string    `json:"alias,omitempty"`
	ResponseModel        string    `json:"response_model,omitempty"`
	ExecutorType         string    `json:"executor_type"`
	Endpoint             string    `json:"endpoint,omitempty"`
	AuthType             string    `json:"auth_type"`
	AuthIndex            string    `json:"auth_index,omitempty"`
	Source               string    `json:"source,omitempty"`
	RequestID            string    `json:"request_id,omitempty"`
	ExecutionID          string    `json:"execution_id"`
	TraceID              string    `json:"trace_id,omitempty"`
	SessionID            string    `json:"session_id,omitempty"`
	ParentSessionID      string    `json:"parent_session_id,omitempty"`
	NodeKind             string    `json:"node_kind,omitempty"`
	IsFork               bool      `json:"is_fork,omitempty"`
	IsCompaction         bool      `json:"is_compaction,omitempty"`
	ReasoningEffort      string    `json:"reasoning_effort,omitempty"`
	ServiceTier          string    `json:"service_tier,omitempty"`
	EffectiveServiceTier string    `json:"effective_service_tier,omitempty"`
	ResponseServiceTier  string    `json:"response_service_tier,omitempty"`
	Generate             bool      `json:"generate"`
	Stream               bool      `json:"stream"`
	Failed               bool      `json:"failed"`
	Fail                 struct {
		StatusCode int `json:"status_code"`
	} `json:"fail"`
	LatencyMS         int64           `json:"latency_ms"`
	TTFTMS            *int64          `json:"ttft_ms,omitempty"`
	Tokens            tokenStats      `json:"tokens"`
	AccountingVersion int             `json:"accounting_version"`
	TokenBreakdown    json.RawMessage `json:"token_breakdown"`
}
