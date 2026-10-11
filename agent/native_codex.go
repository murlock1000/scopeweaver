package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// NativeCodexConfig selects the local Codex app-server worker loop. It is
// deliberately resolved per run so switching a role to any other LLM profile
// immediately returns Worker to the normal provider/agentcore path.
type NativeCodexConfig struct {
	Endpoint        string
	Model           string
	ReasoningEffort string
}

type nativeToolSpec struct {
	Type     string             `json:"type"`
	Function nativeToolFunction `json:"function"`
}

type nativeToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type nativeWorkerRequest struct {
	SystemPrompt    string           `json:"system_prompt"`
	Input           string           `json:"input"`
	SessionID       string           `json:"session_id"`
	CallbackURL     string           `json:"callback_url"`
	CallbackToken   string           `json:"callback_token"`
	Model           string           `json:"model"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	Tools           []nativeToolSpec `json:"tools"`
}

type nativeWorkerResponse struct {
	OK        bool   `json:"ok"`
	Content   string `json:"content"`
	ToolCalls int    `json:"tool_calls"`
	Usage     struct {
		InputTokens           int `json:"input_tokens"`
		CachedInputTokens     int `json:"cached_input_tokens"`
		CacheWriteInputTokens int `json:"cache_write_input_tokens"`
		OutputTokens          int `json:"output_tokens"`
	} `json:"usage"`
}

type nativeToolCall struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type nativeToolResult struct {
	Output  string   `json:"output"`
	IsError bool     `json:"is_error"`
	Extra   []string `json:"extra,omitempty"`
}

var nativeToolNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func nativeWorkerSessionID(explorationID int64, worker string) string {
	worker = regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(worker, "-")
	worker = strings.Trim(worker, "-")
	if worker == "" {
		worker = "worker"
	}
	return fmt.Sprintf("exp%d-native-%s", explorationID, worker)
}

func nativeAlias(name string) string {
	if nativeToolNameRE.MatchString(name) && !strings.HasPrefix(strings.ToLower(name), "mcp__") {
		return name
	}
	clean := regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(name, "_")
	clean = strings.Trim(clean, "_")
	if clean == "" || clean[0] >= '0' && clean[0] <= '9' {
		clean = "tool_" + clean
	}
	sum := sha256.Sum256([]byte(name))
	if len(clean) > 42 {
		clean = clean[:42]
	}
	return "sw_" + clean + "_" + hex.EncodeToString(sum[:4])
}

type rawInputTool interface{ AcceptsRawInput() bool }

type nativeToolHost struct {
	ctx        context.Context
	token      string
	tools      map[string]actool.CoreTool
	hooks      harness.HookRunner
	emit       func(db.Activity)
	workingDir string
	outputDir  string
	agentID    string
	env        []string
	tasks      *actool.Manager
}

func (h *nativeToolHost) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+h.token {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(nativeToolResult{Output: "callback authorization failed", IsError: true})
		return
	}
	defer r.Body.Close()
	var call nativeToolCall
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&call); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(nativeToolResult{Output: "invalid tool call: " + err.Error(), IsError: true})
		return
	}
	result := h.execute(call)
	_ = json.NewEncoder(w).Encode(result)
}

func (h *nativeToolHost) execute(call nativeToolCall) nativeToolResult {
	t, ok := h.tools[call.Name]
	if !ok {
		return nativeToolResult{Output: fmt.Sprintf("Error: unknown tool %q", call.Name), IsError: true}
	}
	input := call.Arguments
	if len(input) == 0 || string(input) == "null" {
		input = json.RawMessage(`{}`)
	}
	if h.emit != nil {
		h.emit(db.Activity{Kind: "tool_use", Tool: t.Name(), ToolUseID: call.CallID,
			Summary: t.Name() + " " + firstLine(string(input), 200), Detail: string(input)})
	}
	finish := func(result nativeToolResult) nativeToolResult {
		if h.emit != nil {
			h.emit(db.Activity{Kind: "tool_result", Tool: t.Name(), ToolUseID: call.CallID,
				IsError: result.IsError, Summary: firstLine(result.Output, 200), Detail: result.Output})
		}
		return result
	}
	if raw, ok := t.(rawInputTool); !ok || !raw.AcceptsRawInput() {
		if err := actool.ValidateInput(t.InputSchema(), input); err != nil {
			return finish(nativeToolResult{Output: "Error: invalid tool input: " + err.Error(), IsError: true})
		}
	}
	pc := permission.Context{Mode: permission.ModeBypass, WorkingDir: h.workingDir}
	decision := permission.Evaluate(h.ctx, permission.Request{
		ToolName: call.Name, Input: input, IsReadOnly: t.IsReadOnly(input),
		ToolDecision: t.CheckPermissions(h.ctx, input, pc), Ctx: pc,
	})
	if decision.Behavior != permission.Allow {
		message := decision.Message
		if message == "" {
			message = "denied"
		}
		return finish(nativeToolResult{Output: "Tool call was not permitted: " + message, IsError: true})
	}
	if len(decision.UpdatedInput) > 0 {
		input = decision.UpdatedInput
	}
	if h.hooks != nil {
		if block, message, updated := h.hooks.PreToolUse(h.ctx, t.Name(), input); block {
			return finish(nativeToolResult{Output: "Blocked by hook: " + message, IsError: true})
		} else if len(updated) > 0 {
			input = updated
		}
	}
	tc := &actool.ToolContext{
		WorkingDir: h.workingDir, AgentID: h.agentID, ToolUseID: call.CallID,
		OutputDir: h.outputDir, MaxOutputChars: 30000, Tasks: h.tasks, Env: h.env,
		Emit: func(info actool.ProgressInfo) {
			if h.emit != nil && strings.TrimSpace(info.Message) != "" {
				h.emit(db.Activity{Kind: "thinking", Tool: t.Name(), ToolUseID: call.CallID,
					Summary: firstLine(info.Message, 200), Detail: info.Message})
			}
		},
	}
	res, err := t.Call(h.ctx, input, tc)
	if err != nil {
		res = actool.Errorf("Error: " + err.Error())
	}
	for i, block := range res.Content {
		if block.Type == llm.BlockText {
			res.Content[i].Text = actool.CaptureOnce(tc, block.Text)
		}
	}
	output := res.Flatten()
	if output == "" {
		output = "(no output)"
	}
	if h.hooks != nil {
		raw, _ := json.Marshal(output)
		h.hooks.PostToolUse(h.ctx, t.Name(), input, raw, res.IsError)
	}
	extras := make([]string, 0, len(res.Extra))
	for _, message := range res.Extra {
		if text := strings.TrimSpace(message.Text()); text != "" {
			extras = append(extras, text)
		}
	}
	return finish(nativeToolResult{Output: output, IsError: res.IsError, Extra: extras})
}

func runNativeCodexWorker(
	ctx context.Context,
	cfg NativeCodexConfig,
	sessionID, systemPrompt, input, worker, runDir string,
	tools []actool.CoreTool,
	hooks harness.HookRunner,
	emit func(db.Activity),
	env []string,
) (harness.TerminalReason, error) {
	parsed, err := url.Parse(strings.TrimRight(cfg.Endpoint, "/"))
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		return harness.ReasonModelError, fmt.Errorf("native Codex endpoint must use loopback HTTP")
	}
	aliases := make(map[string]actool.CoreTool, len(tools))
	specs := make([]nativeToolSpec, 0, len(tools))
	for _, t := range tools {
		alias := nativeAlias(t.Name())
		for aliases[alias] != nil {
			alias += "_x"
		}
		aliases[alias] = t
		description := t.Description()
		if alias != t.Name() {
			description = "ScopeWeaver tool " + t.Name() + ". " + description
		}
		specs = append(specs, nativeToolSpec{Type: "function", Function: nativeToolFunction{
			Name: alias, Description: description, Parameters: t.InputSchema(),
		}})
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return harness.ReasonModelError, err
	}
	token := hex.EncodeToString(tokenBytes)
	tasks, err := actool.NewManager(sessionID)
	if err != nil {
		return harness.ReasonModelError, err
	}
	defer tasks.Cleanup()
	host := &nativeToolHost{ctx: ctx, token: token, tools: aliases, hooks: hooks, emit: emit,
		workingDir: runDir, outputDir: cmdOutDir(runDir), agentID: worker, env: env, tasks: tasks}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return harness.ReasonModelError, err
	}
	server := &http.Server{Handler: http.HandlerFunc(host.serveHTTP), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()) }()

	endpoint := strings.TrimRight(cfg.Endpoint, "/") + "/v1/native/worker/run"
	client := &http.Client{Timeout: 0}
	currentInput := input
	var totalIn, totalOut, totalCacheRead, totalCacheWrite int
	for {
		payload := nativeWorkerRequest{
			SystemPrompt: systemPrompt, Input: currentInput, SessionID: sessionID,
			CallbackURL: "http://" + listener.Addr().String(), CallbackToken: token,
			Model: cfg.Model, ReasoningEffort: cfg.ReasoningEffort, Tools: specs,
		}
		body, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return harness.ReasonModelError, err
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return harness.ReasonAbortedStreaming, ctx.Err()
			}
			return harness.ReasonModelError, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		response.Body.Close()
		if readErr != nil {
			return harness.ReasonModelError, readErr
		}
		if response.StatusCode != http.StatusOK {
			return harness.ReasonModelError, fmt.Errorf("native Codex gateway returned %s: %s", response.Status, strings.TrimSpace(string(raw)))
		}
		var result nativeWorkerResponse
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			if err == nil {
				err = fmt.Errorf("gateway returned ok=false")
			}
			return harness.ReasonModelError, err
		}
		totalIn += result.Usage.InputTokens
		totalOut += result.Usage.OutputTokens
		totalCacheRead += result.Usage.CachedInputTokens
		totalCacheWrite += result.Usage.CacheWriteInputTokens
		if hooks != nil {
			messages := []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(result.Content)}}}
			prevent, blocking, message := hooks.Stop(ctx, messages)
			if prevent {
				if emit != nil {
					emit(db.Activity{Kind: "result", IsError: true, Summary: firstLine(message, 400), Detail: message})
				}
				return harness.ReasonStopHookPrevented, fmt.Errorf("worker stopped by hook: %s", message)
			}
			if len(blocking) > 0 {
				currentInput = "The ScopeWeaver stop hook requires you to continue and resolve these issues before finishing:\n- " + strings.Join(blocking, "\n- ")
				continue
			}
		}
		if emit != nil {
			in, out, cr, cw := totalIn, totalOut, totalCacheRead, totalCacheWrite
			emit(db.Activity{Kind: "result", Summary: firstLine(result.Content, 400), Detail: result.Content,
				InputTokens: &in, OutputTokens: &out, CacheReadTokens: &cr, CacheWriteTokens: &cw})
		}
		return harness.ReasonCompleted, nil
	}
}
