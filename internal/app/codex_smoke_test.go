//go:build windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qc-switch/internal/domain"
	"qc-switch/internal/protocol"
)

// Opt-in real CLI validation against local mock upstreams. Never loads the
// developer's config/auth, calls a paid endpoint, or uses a real API key.
func TestCodexCLIMockUpstreams(t *testing.T) {
	bin := os.Getenv("QCSWITCH_CODEX_EXE")
	if bin == "" {
		t.Skip("set QCSWITCH_CODEX_EXE for isolated real-client smoke validation")
	}
	version, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(strings.TrimSpace(string(version)))
	for _, target := range []domain.Protocol{domain.Responses, domain.Messages, domain.Chat} {
		t.Run(string(target), func(t *testing.T) {
			var calls atomic.Int32
			work := t.TempDir()
			if e := os.WriteFile(filepath.Join(work, "qc-smoke-input.txt"), []byte("QC_READ_OK"), 0600); e != nil {
				t.Fatal(e)
			}
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				step := calls.Add(1)
				var body protocol.Object
				if json.NewDecoder(r.Body).Decode(&body) != nil || !strings.Contains(protocol.JSON(body), "QC_SWITCH_OK") {
					t.Error("client request lost its input")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chat := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"QC_SWITCH_OK\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				decoded, decodeErr := protocol.Decode(target, body)
				if decodeErr != nil {
					t.Error(decodeErr)
				}
				toolctx := protocol.Context{Tools: map[string]protocol.Tool{}}
				for _, tool := range decoded.Tools {
					toolctx.Tools[tool.WireName] = tool
				}
				if step <= 3 {
					var tool protocol.Tool
					args := protocol.Object{}
					for _, candidate := range decoded.Tools {
						properties := protocol.Obj(protocol.Obj(candidate.Schema)["properties"])
						if step <= 2 && (properties["cmd"] != nil || properties["command"] != nil) {
							tool = candidate
							command := "Get-Content -LiteralPath '" + filepath.Join(work, "qc-smoke-input.txt") + "'"
							if step == 2 {
								command = "Write-Output QC_EXEC_OK"
							}
							field := "cmd"
							if properties["command"] != nil {
								field = "command"
							}
							args[field] = command
							if properties["workdir"] != nil {
								args["workdir"] = work
							}
							break
						}
						if step == 3 && (strings.Contains(candidate.Description, "apply_patch") || candidate.Name == "apply_patch") {
							tool = candidate
							args["input"] = "*** Begin Patch\n*** Add File: " + filepath.ToSlash(filepath.Join(work, "qc-smoke-output.txt")) + "\n+QC_PATCH_OK\n*** End Patch"
							break
						}
					}
					if tool.Name == "" {
						t.Errorf("missing tool for step %d", step)
					}
					if step > 1 {
						expected := "QC_READ_OK"
						if step == 3 {
							expected = "QC_EXEC_OK"
						}
						if !strings.Contains(protocol.JSON(body), expected) {
							t.Errorf("missing tool result %s", expected)
						}
					}
					chat = "data: " + protocol.JSON(protocol.Object{"choices": []any{protocol.Object{"index": 0, "delta": protocol.Object{"tool_calls": []any{protocol.Object{"index": 0, "id": fmt.Sprintf("call_%d", step), "function": protocol.Object{"name": tool.WireName, "arguments": protocol.JSON(args)}}}}, "finish_reason": "tool_calls"}}}) + "\n\ndata: [DONE]\n\n"
				}
				if target == domain.Chat {
					fmt.Fprint(w, chat)
					return
				}
				s := protocol.NewStream(target, "qc-test-model", fmt.Sprintf("test_response_%d", step), toolctx, func(event string, data protocol.Object) error {
					_, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, protocol.JSON(data))
					return e
				})
				if e := s.Run(domain.Chat, strings.NewReader(chat)); e != nil {
					t.Error(e)
				}
			}))
			defer up.Close()
			a, e := New(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			call(t, a, "profiles.save", domain.Profile{ID: "smoke", Name: "Mock only", Endpoint: up.URL, Protocol: target, Key: "mock-only", Models: []domain.Model{{ID: "qc-test-model", Tools: true, Context: 32000}}})
			call(t, a, "settings.save", domain.Settings{Port: 0, Theme: "system", RetentionDays: 30})
			configDir := t.TempDir()
			call(t, a, "clients.apply", domain.Binding{Client: "codex", ProfileID: "smoke", Model: "qc-test-model", Mode: "route", Directory: configDir})
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			// Only the deterministic mock above supplies tool calls, and every file
			// action is confined to this test's temporary directory. Permit those
			// actions without an interactive approval prompt or OS sandbox setup.
			cmd := exec.CommandContext(ctx, bin, "exec", "--strict-config", "--ephemeral", "--skip-git-repo-check", "--ignore-rules", "--dangerously-bypass-approvals-and-sandbox", "--json", "--color", "never", "Read qc-smoke-input.txt, run a harmless command, create qc-smoke-output.txt using apply_patch, then reply QC_SWITCH_OK.")
			cmd.Dir = work
			for _, env := range os.Environ() {
				name, _, _ := strings.Cut(env, "=")
				if strings.EqualFold(name, "CODEX_HOME") || strings.HasPrefix(strings.ToUpper(name), "OPENAI_") || strings.EqualFold(name, "CODEX_API_KEY") {
					continue
				}
				cmd.Env = append(cmd.Env, env)
			}
			cmd.Env = append(cmd.Env, "CODEX_HOME="+configDir, "DO_NOT_TRACK=1", "OTEL_SDK_DISABLED=true")
			output, e := cmd.CombinedOutput()
			if e != nil || !strings.Contains(string(output), `"text":"QC_SWITCH_OK"`) || calls.Load() == 0 {
				t.Fatalf("CLI mock smoke failed: %v; calls=%d\n%s", e, calls.Load(), output)
			}
			patched, e := os.ReadFile(filepath.Join(work, "qc-smoke-output.txt"))
			if e != nil || !strings.Contains(string(patched), "QC_PATCH_OK") || calls.Load() != 4 {
				t.Fatalf("patch/multiple rounds failed: %v; calls=%d\n%s", e, calls.Load(), output)
			}
			t.Logf("mock requests: %d; catalog/text/stream/read/command/apply_patch/multiple rounds accepted", calls.Load())
		})
	}
}
