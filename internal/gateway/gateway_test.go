//go:build windows

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"qc-switch/internal/domain"
	"qc-switch/internal/protocol"
	"qc-switch/internal/store"
)

func testGateway(t *testing.T) *Gateway {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	g := New(s)
	t.Cleanup(func() { g.Stop(context.Background()); s.Close() })
	return g
}
func resultFor(p domain.Protocol) protocol.Object {
	r := protocol.Result{ID: "r", Model: "m", Stop: "tool_calls", Input: 8, Output: 4, UsageKnown: true, Parts: []protocol.Part{{Kind: "text", Text: "中文"}, {Kind: "call", ID: "c", Name: "read", Args: "{\"path\":\"x\"}"}}}
	if p == domain.Chat {
		return protocol.Object{"id": "r", "model": "m", "choices": []any{protocol.Object{"index": 0, "message": protocol.Object{"role": "assistant", "content": "中文", "tool_calls": []any{protocol.Object{"id": "c", "type": "function", "function": protocol.Object{"name": "read", "arguments": "{\"path\":\"x\"}"}}}}, "finish_reason": "tool_calls"}}, "usage": protocol.Object{"prompt_tokens": 8, "completion_tokens": 4}}
	}
	return protocol.RenderResult(p, r, protocol.Context{})
}
func fixtureStream(p domain.Protocol) string {
	if p != domain.Chat {
		var b strings.Builder
		s := protocol.NewStream(p, "m", "r", protocol.Context{}, func(n string, o protocol.Object) error {
			fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", n, protocol.JSON(o))
			return nil
		})
		// Normalize a fragmented Chat fixture to create valid source SSE fixtures.
		if e := s.Run(domain.Chat, strings.NewReader(fixtureStream(domain.Chat))); e != nil {
			panic(e)
		}
		return b.String()
	}
	return "data: " + protocol.JSON(protocol.Object{"choices": []any{protocol.Object{"index": 0, "delta": protocol.Object{"content": "中文", "tool_calls": []any{protocol.Object{"index": 0, "id": "c", "function": protocol.Object{"name": "read", "arguments": "{\"path\":"}}}}}}}) + "\n\ndata: " + protocol.JSON(protocol.Object{"choices": []any{protocol.Object{"index": 0, "delta": protocol.Object{"tool_calls": []any{protocol.Object{"index": 0, "function": protocol.Object{"arguments": "\"x\"}"}}}}, "finish_reason": "tool_calls"}}}) + "\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"
}
func TestSixRoutesJSONStreamingAndReplay(t *testing.T) {
	for _, source := range []domain.Protocol{domain.Messages, domain.Responses} {
		for _, target := range []domain.Protocol{domain.Messages, domain.Responses, domain.Chat} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", source, target, stream), func(t *testing.T) {
					var calls atomic.Int32
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.URL.Path != Path(target) || r.Header.Get("Authorization") != "Bearer real-key" {
							t.Error("wrong upstream route/auth")
						}
						var b protocol.Object
						json.NewDecoder(r.Body).Decode(&b)
						if b["model"] != "m" || !strings.Contains(protocol.JSON(b), "工具結果") {
							t.Error("replay missing", b)
						}
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							for _, v := range []byte(fixtureStream(target)) {
								w.Write([]byte{v})
							}
							return
						}
						json.NewEncoder(w).Encode(resultFor(target))
					}))
					defer up.Close()
					g := testGateway(t)
					client := "codex"
					if source == domain.Messages {
						client = "claude"
					}
					g.Set(Route{Token: "local-token", Binding: domain.Binding{Client: client, Model: "m"}, Profile: domain.Profile{Name: "test", Endpoint: up.URL, Key: "real-key", Protocol: target, Models: []domain.Model{{ID: "m"}}}})
					address, e := g.Start(0)
					if e != nil {
						t.Fatal(e)
					}
					canonical := protocol.Request{Model: "m", Stream: stream, Extras: protocol.Object{}, Tools: []protocol.Tool{{Name: "read", WireName: "read", Kind: "function", Schema: protocol.Object{"type": "object"}}}, Messages: []protocol.Message{{Role: "user", Parts: []protocol.Part{{Kind: "text", Text: "read x"}}}, {Role: "assistant", Parts: []protocol.Part{{Kind: "call", ID: "previous", Name: "read", Args: "{}"}}}, {Role: "tool", Parts: []protocol.Part{{Kind: "result", ID: "previous", Data: []protocol.Part{{Kind: "text", Text: "工具結果"}}}}}, {Role: "user", Parts: []protocol.Part{{Kind: "text", Text: "繼續"}}}}}
					body, _, _ := protocol.Encode(source, canonical)
					req, _ := http.NewRequest("POST", address+Path(source), strings.NewReader(protocol.JSON(body)))
					req.Header.Set("Authorization", "Bearer local-token")
					resp, e := http.DefaultClient.Do(req)
					if e != nil {
						t.Fatal(e)
					}
					defer resp.Body.Close()
					if resp.StatusCode != 200 {
						b, _ := io.ReadAll(resp.Body)
						t.Fatal(resp.StatusCode, string(b))
					}
					var result protocol.Result
					if stream {
						s := protocol.NewStream(domain.Responses, "m", "r", protocol.Context{}, func(string, protocol.Object) error { return nil })
						if e = s.Run(source, resp.Body); e != nil {
							t.Fatal(e)
						}
						result = s.Result
					} else {
						var b protocol.Object
						json.NewDecoder(resp.Body).Decode(&b)
						result, e = protocol.ParseResult(source, b)
						if e != nil {
							t.Fatal(e)
						}
					}
					if protocol.Text(result.Parts) != "中文" || len(result.Parts) != 2 || result.Parts[1].Name != "read" || result.Input != 8 || result.Output != 4 {
						t.Fatalf("bad response: %#v", result)
					}
					if calls.Load() != 1 {
						t.Fatal("request repeated")
					}
				})
			}
		}
	}
}

func TestStalledStreamCancelsUpstreamWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	cancelled := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"開始\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer up.Close()
	g := testGateway(t)
	g.IdleTimeout = 50 * time.Millisecond
	g.Set(Route{Token: "local", Binding: domain.Binding{Client: "codex", Model: "m"}, Profile: domain.Profile{Name: "test", Endpoint: up.URL, Key: "mock", Protocol: domain.Chat}})
	address, err := g.Start(0)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", address+"/v1/responses", strings.NewReader(`{"model":"m","stream":true,"input":"hi"}`))
	req.Header.Set("Authorization", "Bearer local")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Contains(body, []byte("response.failed")) || bytes.Contains(body, []byte("response.completed")) {
		t.Fatalf("stalled stream incorrectly completed: %s / %v", body, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream not cancelled")
	}
	if calls.Load() != 1 {
		t.Fatal("stalled request retried")
	}
	records, err := g.Store.Records(10)
	if err != nil || len(records) != 1 || records[0].Status != 502 {
		t.Fatalf("missing failure record: %v / %v", records, err)
	}
}

func TestAuthTruncationCancellationAndHotSwitch(t *testing.T) {
	g := testGateway(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var b protocol.Object
		json.NewDecoder(r.Body).Decode(&b)
		if b["model"] == "old" {
			close(entered)
			<-release
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer up.Close()
	route := Route{Token: "local", Binding: domain.Binding{Client: "codex", Model: "old"}, Profile: domain.Profile{Name: "p", Protocol: domain.Chat, Key: "real", Endpoint: up.URL}}
	g.Set(route)
	address, _ := g.Start(0)
	resp, e := http.Post(address+"/v1/responses", "application/json", strings.NewReader("{}"))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || calls.Load() != 0 {
		t.Fatal("unauthenticated request forwarded")
	}
	finished := make(chan []byte)
	go func() {
		req, _ := http.NewRequest("POST", address+"/v1/responses", strings.NewReader(`{"stream":true,"input":"hello"}`))
		req.Header.Set("Authorization", "Bearer local")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			finished <- nil
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		finished <- b
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no upstream request")
	}
	route.Binding.Model = "new"
	g.Set(route)
	close(release)
	select {
	case b := <-finished:
		if !bytes.Contains(b, []byte("response.failed")) || bytes.Contains(b, []byte("response.completed")) {
			t.Fatal(string(b))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hung stream")
	}
	if calls.Load() != 1 {
		t.Fatal("truncated request retried")
	}
	// Client cancellation reaches upstream promptly.
	cancelled := make(chan struct{})
	cancelUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"start\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer cancelUp.Close()
	route.Profile.Endpoint = cancelUp.URL
	g.Set(route)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", address+"/v1/responses", strings.NewReader(`{"stream":true,"input":"hello"}`))
	req.Header.Set("Authorization", "Bearer local")
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream not cancelled")
	}
}

func TestEndpointNormalization(t *testing.T) {
	for _, s := range []string{"https://quickrouter.ai", "https://quickrouter.ai/", "https://quickrouter.ai/v1/"} {
		out, e := Endpoint(s)
		if e != nil || out != "https://quickrouter.ai" {
			t.Fatal(out, e)
		}
	}
	for _, s := range []string{"http://example.com", "https://u:p@example.com", "https://example.com/path", "https://example.com?key=secret"} {
		if _, e := Endpoint(s); e == nil {
			t.Fatal("accepted", s)
		}
	}
}
