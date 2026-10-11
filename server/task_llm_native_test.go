package server

import (
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/llm"
)

func TestNativeCodexProfileSelection(t *testing.T) {
	cfg := agent.Config{Format: llm.FormatOpenAI, BaseURL: "http://127.0.0.1:8791/v1", Model: "gpt-daybreak-blue-latest", ReasoningEffort: "xhigh", SessionHeaderKey: "X-ScopeWeaver-Session"}
	got, ok := nativeCodexFromConfig(cfg)
	if !ok || got.Endpoint != "http://127.0.0.1:8791" || got.ReasoningEffort != "xhigh" {
		t.Fatalf("native selection = %+v, %v", got, ok)
	}
}

func TestNonCodexProfilesKeepStandardWorkerPath(t *testing.T) {
	cases := []agent.Config{
		{Format: llm.FormatOpenAI, BaseURL: "https://ollama.com/v1", Model: "deepseek-v4.1-flash"},
		{Format: llm.FormatOpenAI, BaseURL: "http://127.0.0.1:8791/v1", Model: "other", SessionHeaderKey: ""},
		{Format: llm.FormatAnthropic, BaseURL: "http://127.0.0.1:8791/v1", Model: "claude", SessionHeaderKey: "X-ScopeWeaver-Session"},
	}
	for _, cfg := range cases {
		if got, ok := nativeCodexFromConfig(cfg); ok {
			t.Fatalf("profile unexpectedly selected native path: %+v -> %+v", cfg, got)
		}
	}
}
