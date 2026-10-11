package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

type nativeTestHooks struct {
	pre  int
	post int
}

func (h *nativeTestHooks) PreToolUse(_ context.Context, _ string, input []byte) (bool, string, []byte) {
	h.pre++
	return false, "", input
}
func (h *nativeTestHooks) PostToolUse(_ context.Context, _ string, _, _ []byte, _ bool) { h.post++ }
func (h *nativeTestHooks) Stop(_ context.Context, _ []llm.Message) (bool, []string, string) {
	return false, nil, ""
}

var _ harness.HookRunner = (*nativeTestHooks)(nil)

func TestNativeWorkerSessionUsesStableSlot(t *testing.T) {
	if got := nativeWorkerSessionID(42, "work#2"); got != "exp42-native-work-2" {
		t.Fatalf("session id = %q", got)
	}
}

func TestNativeAliasRewritesReservedMCPNames(t *testing.T) {
	a := nativeAlias("mcp__ScopeSentry__list_projects")
	if a == "mcp__ScopeSentry__list_projects" || !nativeToolNameRE.MatchString(a) {
		t.Fatalf("unsafe alias %q", a)
	}
	if a != nativeAlias("mcp__ScopeSentry__list_projects") {
		t.Fatal("alias is not deterministic")
	}
}

func TestNativeToolHostRunsHooksAndEmitsActivity(t *testing.T) {
	tool := actool.Build(actool.Spec{
		Name: "echo", Schema: map[string]any{"type": "object", "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		ReadOnly: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(_ context.Context, input json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return actool.Text(string(input)), nil
		},
	})
	hooks := &nativeTestHooks{}
	var activity []db.Activity
	host := nativeToolHost{ctx: context.Background(), tools: map[string]actool.CoreTool{"echo": tool}, hooks: hooks, emit: func(a db.Activity) { activity = append(activity, a) }}
	result := host.execute(nativeToolCall{CallID: "c1", Name: "echo", Arguments: json.RawMessage(`{"value":"ok"}`)})
	if result.IsError || result.Output != `{"value":"ok"}` {
		t.Fatalf("result = %+v", result)
	}
	if hooks.pre != 1 || hooks.post != 1 {
		t.Fatalf("hooks pre=%d post=%d", hooks.pre, hooks.post)
	}
	if len(activity) != 2 || activity[0].Kind != "tool_use" || activity[1].Kind != "tool_result" {
		t.Fatalf("activity = %+v", activity)
	}
}
