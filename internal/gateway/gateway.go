package gateway

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"qc-switch/internal/config"
	"qc-switch/internal/domain"
	"qc-switch/internal/protocol"
	"qc-switch/internal/store"
)

type Route struct {
	Binding domain.Binding
	Profile domain.Profile
	Token   string
}
type Gateway struct {
	mu       sync.RWMutex
	routes   map[string]Route
	server   *http.Server
	listener net.Listener
	active   atomic.Int64
	Store    *store.Store
	Client   *http.Client
	Changed  func()
	// IdleTimeout bounds a stalled upstream body without limiting the total
	// duration of an otherwise active generation.
	IdleTimeout time.Duration
}

func New(s *store.Store) *Gateway {
	return &Gateway{Store: s, routes: map[string]Route{}, IdleTimeout: 180 * time.Second, Client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 120 * time.Second, IdleConnTimeout: 90 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type idleBody struct {
	io.ReadCloser
	timer *time.Timer
	idle  time.Duration
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	return n, err
}
func (b *idleBody) Close() error {
	b.timer.Stop()
	return b.ReadCloser.Close()
}
func Endpoint(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("endpoint must be an HTTPS root URL or /v1 base URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return "", errors.New("HTTPS is required except for loopback test servers")
		}
	}
	path := strings.TrimRight(u.Path, "/")
	if path != "" && path != "/v1" {
		return "", errors.New("endpoint path must be empty or /v1")
	}
	u.Path = strings.TrimSuffix(path, "/v1")
	return strings.TrimRight(u.String(), "/"), nil
}
func Path(p domain.Protocol) string {
	switch p {
	case domain.Messages:
		return "/v1/messages"
	case domain.Responses:
		return "/v1/responses"
	default:
		return "/v1/chat/completions"
	}
}
func (g *Gateway) Status() (bool, string, int64) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.listener == nil {
		return false, "", g.active.Load()
	}
	return true, "http://" + g.listener.Addr().String(), g.active.Load()
}
func (g *Gateway) Start(port int) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.listener != nil {
		return "http://" + g.listener.Addr().String(), nil
	}
	ln, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		return "", e
	}
	g.listener = ln
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	server := g.server
	go func() { _ = server.Serve(ln) }()
	return "http://" + ln.Addr().String(), nil
}
func (g *Gateway) Set(route Route) {
	g.mu.Lock()
	g.routes[route.Binding.Client] = route
	g.mu.Unlock()
}
func (g *Gateway) Remove(client string) { g.mu.Lock(); delete(g.routes, client); g.mu.Unlock() }
func (g *Gateway) RouteCount() int      { g.mu.RLock(); defer g.mu.RUnlock(); return len(g.routes) }
func (g *Gateway) Stop(ctx context.Context) error {
	g.mu.Lock()
	srv := g.server
	g.mu.Unlock()
	if srv == nil {
		return nil
	}
	e := srv.Shutdown(ctx)
	if e != nil {
		_ = srv.Close()
	}
	g.mu.Lock()
	g.server = nil
	g.listener = nil
	g.mu.Unlock()
	return e
}
func (g *Gateway) notify() {
	if g.Changed != nil {
		g.Changed()
	}
}
func errorJSON(w http.ResponseWriter, p domain.Protocol, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.Object{"type": "error", "error": protocol.Object{"type": "api_error", "message": message}})
}
func cleanError(e error, key string) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	if key != "" {
		s = strings.ReplaceAll(s, key, "[REDACTED]")
	}
	if len(s) > 600 {
		s = s[:600]
	}
	return s
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client := ""
	source := domain.Responses
	switch r.URL.Path {
	case "/v1/messages":
		client = "claude"
		source = domain.Messages
	case "/v1/responses":
		client = "codex"
	default:
		http.NotFound(w, r)
		return
	}
	g.mu.RLock()
	route, ok := g.routes[client]
	g.mu.RUnlock()
	if !ok {
		errorJSON(w, source, 503, "此客戶端尚未啟用本地路由")
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if provided == "" {
		provided = r.Header.Get("x-api-key")
	}
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(provided), []byte(route.Token)) != 1 {
		errorJSON(w, source, 401, "本機路由認證失敗")
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		errorJSON(w, source, 405, "POST is required")
		return
	}
	var body protocol.Object
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	if e := decoder.Decode(&body); e != nil || body == nil {
		errorJSON(w, source, 400, "無效或過大的 JSON 請求")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		errorJSON(w, source, 400, "請求只能包含一個 JSON 物件")
		return
	}
	model := route.Binding.Model
	requested := protocol.Str(body["model"])
	if client == "claude" {
		for role, mapped := range route.Binding.Roles {
			if strings.Contains(strings.ToLower(requested), role) && mapped != "" {
				model = mapped
				break
			}
		}
	}
	for _, m := range route.Profile.Models {
		if m.ID == requested {
			model = requested
			break
		}
	}
	body["model"] = model
	g.active.Add(1)
	g.notify()
	defer func() { g.active.Add(-1); g.notify() }()
	start := time.Now()
	record := domain.Record{Client: client, Profile: route.Profile.Name, Model: model, Status: 200}
	defer func() {
		record.DurationMS = time.Since(start).Milliseconds()
		_ = g.Store.AddRecord(record)
		g.notify()
	}()
	stream := body["stream"] == true
	up := body
	ctx := protocol.Context{}
	if source != route.Profile.Protocol {
		var e error
		up, ctx, e = protocol.Convert(source, route.Profile.Protocol, body)
		if e != nil {
			record.Status = 400
			record.Error = e.Error()
			errorJSON(w, source, 400, e.Error())
			return
		}
	}
	data, e := json.Marshal(up)
	if e != nil {
		record.Status = 400
		errorJSON(w, source, 400, "請求序列化失敗")
		return
	}
	upstreamContext, cancelUpstream := context.WithCancel(r.Context())
	defer cancelUpstream()
	req, e := http.NewRequestWithContext(upstreamContext, "POST", route.Profile.Endpoint+Path(route.Profile.Protocol), bytes.NewReader(data))
	if e != nil {
		record.Status = 500
		errorJSON(w, source, 500, "無效的上游地址")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+route.Profile.Key)
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if route.Profile.Protocol == domain.Messages {
		req.Header.Set("anthropic-version", "2023-06-01")
		if v := r.Header.Get("anthropic-beta"); v != "" && source == domain.Messages {
			req.Header.Set("anthropic-beta", v)
		}
	}
	resp, e := g.Client.Do(req)
	if e != nil {
		record.Status = 502
		record.Error = cleanError(e, route.Profile.Key)
		errorJSON(w, source, 502, record.Error)
		return
	}
	if g.IdleTimeout > 0 {
		resp.Body = &idleBody{ReadCloser: resp.Body, timer: time.AfterFunc(g.IdleTimeout, cancelUpstream), idle: g.IdleTimeout}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		record.Status = resp.StatusCode
		record.Error = fmt.Sprintf("上游 HTTP %d", resp.StatusCode)
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		if id := resp.Header.Get("x-request-id"); id != "" {
			w.Header().Set("x-request-id", id)
		}
		errorJSON(w, source, resp.StatusCode, record.Error)
		return
	}
	if !stream {
		var result protocol.Object
		if e = json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&result); e != nil {
			record.Status = 502
			record.Error = "上游回應不是有效 JSON"
			errorJSON(w, source, 502, record.Error)
			return
		}
		parsed, parseErr := protocol.ParseResult(route.Profile.Protocol, result)
		record.InputTokens = parsed.Input
		record.OutputTokens = parsed.Output
		record.UsageKnown = parsed.UsageKnown
		if result["error"] != nil || protocol.Str(result["status"]) == "failed" {
			record.Status = 502
			record.Error = "上游回應失敗"
			errorJSON(w, source, 502, record.Error)
			return
		}
		if source != route.Profile.Protocol {
			if parseErr != nil {
				record.Status = 502
				record.Error = parseErr.Error()
				errorJSON(w, source, 502, record.Error)
				return
			}
			parsed.Model = model
			result = protocol.RenderResult(source, parsed, ctx)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(name string, o protocol.Object) error {
		_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, protocol.JSON(o)); err != nil {
			return err
		}
		return controller.Flush()
	}
	if source == route.Profile.Protocol {
		terminal := false
		e = protocol.ReadSSE(resp.Body, func(event string, data []byte) (bool, error) {
			var o protocol.Object
			if err := json.Unmarshal(data, &o); err != nil {
				return false, errors.New("上游傳送無效 SSE JSON")
			}
			if event == "" {
				event = protocol.Str(o["type"])
			}
			if err := write(event, o); err != nil {
				return false, err
			}
			if source == domain.Responses {
				if event == "response.failed" || event == "error" {
					terminal = true
					return true, errors.New("上游回應失敗")
				}
				if event == "response.completed" || event == "response.incomplete" {
					u := protocol.Obj(protocol.Obj(o["response"])["usage"])
					record.InputTokens = protocol.Num(u["input_tokens"])
					record.OutputTokens = protocol.Num(u["output_tokens"])
					record.UsageKnown = len(u) > 0
					terminal = true
					return true, nil
				}
			}
			if source == domain.Messages {
				if event == "error" {
					terminal = true
					return true, errors.New("上游回應失敗")
				}
				if event == "message_start" {
					u := protocol.Obj(protocol.Obj(o["message"])["usage"])
					record.InputTokens = protocol.Num(u["input_tokens"])
					record.UsageKnown = len(u) > 0
				}
				if event == "message_delta" {
					u := protocol.Obj(o["usage"])
					record.OutputTokens = protocol.Num(u["output_tokens"])
					record.UsageKnown = record.UsageKnown || len(u) > 0
				}
				if event == "message_stop" {
					terminal = true
					return true, nil
				}
			}
			return false, nil
		})
		if e != nil && !terminal {
			failure := protocol.NewStream(source, model, "resp_"+config.ID(), ctx, write)
			_ = failure.Fail("上游串流中斷；未完成此回應")
		}
	} else {
		converter := protocol.NewStream(source, model, "resp_"+config.ID(), ctx, write)
		e = converter.Run(route.Profile.Protocol, resp.Body)
		record.InputTokens = converter.Result.Input
		record.OutputTokens = converter.Result.Output
		record.UsageKnown = converter.Result.UsageKnown
		if e != nil {
			_ = converter.Fail("上游串流中斷或協議不相容；未完成此回應")
		}
	}
	if e != nil {
		record.Status = 502
		if r.Context().Err() != nil {
			record.Status = 499
		}
		record.Error = cleanError(e, route.Profile.Key)
	}
}
