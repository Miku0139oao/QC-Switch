package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"qc-switch/internal/domain"
)

type byteReader struct{ io.Reader }

func (r byteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }
func event(kind string, b Object) string {
	return "event: " + kind + "\r\ndata: " + JSON(b) + "\r\n\r\n"
}
func chatDelta(d Object) string {
	return event("", Object{"choices": []any{Object{"index": 0, "delta": d}}})
}

func TestStreamFragmentedChineseParallelTools(t *testing.T) {
	for _, target := range []domain.Protocol{domain.Messages, domain.Responses} {
		t.Run(string(target), func(t *testing.T) {
			input := ": heartbeat\r\n\r\n" + chatDelta(Object{"content": "中文🙂"})
			input += chatDelta(Object{"tool_calls": []any{Object{"index": 0, "id": "a", "function": Object{"name": "read", "arguments": "{\"path\":"}}, Object{"index": 1, "id": "b", "function": Object{"name": "exec", "arguments": "{\"command\":"}}}})
			input += chatDelta(Object{"tool_calls": []any{Object{"index": 1, "function": Object{"arguments": "\"dir\"}"}}, Object{"index": 0, "function": Object{"arguments": "\"檔案\"}"}}}})
			input += event("", Object{"choices": []any{Object{"index": 0, "delta": Object{}, "finish_reason": "tool_calls"}}})
			input += event("", Object{"choices": []any{}, "usage": Object{"prompt_tokens": 13, "completion_tokens": 7}}) + "data: [DONE]\r\n\r\n"
			names := []string{}
			s := NewStream(target, "test", "r", Context{}, func(name string, b Object) error { names = append(names, name); return nil })
			if err := s.Run(domain.Chat, byteReader{strings.NewReader(input)}); err != nil {
				t.Fatal(err)
			}
			if Text(s.Result.Parts) != "中文🙂" || len(s.Result.Parts) != 3 || s.Result.Input != 13 || s.Result.Output != 7 {
				t.Fatalf("bad result: %#v", s.Result)
			}
			if s.Result.Parts[1].Args != "{\"path\":\"檔案\"}" || s.Result.Parts[2].Args != "{\"command\":\"dir\"}" {
				t.Fatal(s.Result.Parts)
			}
			terminal := "response.completed"
			if target == domain.Messages {
				terminal = "message_stop"
			}
			if names[len(names)-1] != terminal {
				t.Fatal(names)
			}
		})
	}
}

func TestStreamNeverCompletesTruncatedOrInvalidTools(t *testing.T) {
	cases := []string{chatDelta(Object{"content": "partial"}), chatDelta(Object{"tool_calls": []any{Object{"index": 0, "id": "a", "function": Object{"name": "read", "arguments": "{"}}}}) + "data: [DONE]\n\n", event("response.failed", Object{"type": "response.failed"})}
	for i, input := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			completed := false
			s := NewStream(domain.Responses, "m", "r", Context{}, func(n string, _ Object) error {
				if n == "response.completed" {
					completed = true
				}
				return nil
			})
			source := domain.Chat
			if i == 2 {
				source = domain.Responses
			}
			if s.Run(source, strings.NewReader(input)) == nil || completed {
				t.Fatal("truncated response accepted")
			}
		})
	}
}

func TestCustomToolAndNamespacesFullReplay(t *testing.T) {
	input := Object{"model": "m", "input": []any{
		Object{"role": "developer", "content": "be precise"}, Object{"role": "user", "content": []any{Object{"type": "input_text", "text": "fix"}, Object{"type": "input_image", "image_url": "data:image/png;base64,YQ=="}}},
		Object{"type": "custom_tool_call", "call_id": "patch1", "name": "apply_patch", "input": "*** Begin Patch\n*** End Patch"},
		Object{"type": "function_call", "call_id": "shell1", "namespace": "functions", "name": "shell", "arguments": "{\"cmd\":\"dir\"}"},
		Object{"type": "custom_tool_call_output", "call_id": "patch1", "output": "success"}, Object{"type": "function_call_output", "call_id": "shell1", "output": "file.txt"}, Object{"role": "user", "content": "continue"},
	}, "tools": []any{Object{"type": "custom", "name": "apply_patch", "format": Object{"type": "grammar", "syntax": "lark", "definition": "start: /.+/"}}, Object{"type": "namespace", "name": "functions", "tools": []any{Object{"type": "function", "name": "shell", "parameters": Object{"type": "object"}}}}}}
	for _, p := range []domain.Protocol{domain.Messages, domain.Chat} {
		t.Run(string(p), func(t *testing.T) {
			b, ctx, e := Convert(domain.Responses, p, input)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(JSON(b), "be precise") || !strings.Contains(JSON(b), "file.txt") || !strings.Contains(JSON(b), "base64,YQ==") && p == domain.Chat {
				t.Fatal(JSON(b))
			}
			if p == domain.Chat {
				found := false
				for _, v := range Arr(b["messages"]) {
					m := Obj(v)
					if Str(m["role"]) == "assistant" {
						if len(Arr(m["tool_calls"])) != 2 {
							t.Fatal("parallel calls split", JSON(b))
						}
						found = true
					}
				}
				if !found {
					t.Fatal(b)
				}
			}
			var patch Tool
			for _, v := range ctx.Tools {
				if v.Kind == "custom" {
					patch = v
				}
			}
			result := Result{ID: "r", Model: "m", Parts: []Part{{Kind: "call", ID: "p2", Name: patch.WireName, Args: JSON(Object{"input": "*** Begin Patch\n*** End Patch"})}}}
			output := RenderResult(domain.Responses, result, ctx)
			item := Obj(Arr(output["output"])[0])
			if item["type"] != "custom_tool_call" || item["input"] != "*** Begin Patch\n*** End Patch" {
				t.Fatal(item)
			}
			s := NewStream(domain.Responses, "m", "r", ctx, func(_ string, _ Object) error { return nil })
			wire := chatDelta(Object{"tool_calls": []any{Object{"index": 0, "id": "p2", "function": Object{"name": patch.WireName, "arguments": result.Parts[0].Args}}}}) + "data: [DONE]\n\n"
			if e = s.Run(domain.Chat, strings.NewReader(wire)); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestSixProtocolRequestPaths(t *testing.T) {
	for _, from := range []domain.Protocol{domain.Messages, domain.Responses} {
		for _, to := range []domain.Protocol{domain.Messages, domain.Responses, domain.Chat} {
			t.Run(string(from)+"/"+string(to), func(t *testing.T) {
				canonical := Request{Model: "m", Stream: true, System: "system", MaxTokens: 100, Effort: "high", Extras: Object{}, Messages: []Message{{Role: "user", Parts: []Part{{Kind: "text", Text: "中文"}, {Kind: "image", URL: "data:image/png;base64,YQ=="}}}, {Role: "assistant", Parts: []Part{{Kind: "call", ID: "c", Name: "read", Args: "{\"path\":\"x\"}"}}}, {Role: "tool", Parts: []Part{{Kind: "result", ID: "c", Data: []Part{{Kind: "text", Text: "ok"}}}}}, {Role: "user", Parts: []Part{{Kind: "text", Text: "下一回合"}}}}, Tools: []Tool{{Name: "read", WireName: "read", Kind: "function", Schema: Object{"type": "object"}}}}
				source, _, err := Encode(from, canonical)
				if err != nil {
					t.Fatal(err)
				}
				out, _, err := Convert(from, to, source)
				if err != nil {
					t.Fatal(err)
				}
				data := []byte(JSON(out))
				if !bytes.Contains(data, []byte("中文")) || !bytes.Contains(data, []byte("下一回合")) || !bytes.Contains(data, []byte("read")) {
					t.Fatal(string(data))
				}
				if !json.Valid(data) {
					t.Fatal("invalid JSON")
				}
			})
		}
	}
}

func TestReadSSEMultilineAndWriteCancellation(t *testing.T) {
	e := ReadSSE(byteReader{strings.NewReader("data: {\n: ping\ndata: \"v\":1}\n\n")}, func(_ string, b []byte) (bool, error) {
		if !json.Valid(b) {
			t.Fatal(string(b))
		}
		return true, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	s := NewStream(domain.Messages, "m", "r", Context{}, func(string, Object) error { return io.ErrClosedPipe })
	if s.Run(domain.Chat, strings.NewReader(chatDelta(Object{"content": "x"}))) != io.ErrClosedPipe {
		t.Fatal("write cancellation lost")
	}
}
