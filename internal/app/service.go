package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"qc-switch/internal/config"
	"qc-switch/internal/domain"
	"qc-switch/internal/gateway"
	"qc-switch/internal/protocol"
	"qc-switch/internal/store"
)

const Version = "0.1.0"

type Service struct {
	mu       sync.Mutex
	Store    *store.Store
	Config   *config.Manager
	Gateway  *gateway.Gateway
	settings domain.Settings
	recovery []string
	Notify   func()
}

func New(dir string) (*Service, error) {
	s, e := store.Open(dir)
	if e != nil {
		return nil, e
	}
	a := &Service{Store: s, Config: &config.Manager{Store: s}, settings: domain.Settings{Port: 15722, Theme: "system", RetentionDays: 30}}
	a.Gateway = gateway.New(s)
	a.Gateway.Changed = func() {
		if a.Notify != nil {
			a.Notify()
		}
	}
	_ = s.Get("settings", &a.settings)
	a.recovery = a.Config.Recover()
	_ = s.Prune(a.settings.RetentionDays)
	return a, nil
}
func (a *Service) state() (domain.State, error) {
	profiles, e := a.Store.Profiles()
	if e != nil {
		return domain.State{}, e
	}
	running, address, active := a.Gateway.Status()
	state := domain.State{Version: Version, Contract: 1, Profiles: profiles, Settings: a.settings, Running: running, Address: address, Active: active, Clients: []domain.ClientState{}, Records: []domain.Record{}, DataDirectory: a.Store.Dir, Recovery: a.recovery}
	for _, c := range []string{"claude", "codex"} {
		var b domain.Binding
		_ = a.Store.Get("binding."+c, &b)
		cs := config.Diagnostics(c, b.Directory)
		if b.Client != "" {
			copy := b
			cs.Binding = &copy
		}
		j, e := a.Config.Current(c)
		if e != nil {
			cs.Warnings = append(cs.Warnings, e.Error())
		}
		cs.Managed = j != nil
		state.Clients = append(state.Clients, cs)
	}
	state.Records, e = a.Store.Records(100)
	return state, e
}
func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, v)
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func validate(p *domain.Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return errors.New("請輸入連線名稱")
	}
	if p.ID == "" {
		p.ID = config.ID()
	}
	if !validID.MatchString(p.ID) {
		return errors.New("無效的設定識別碼")
	}
	if !p.Protocol.Valid() {
		return errors.New("請選擇有效的接口協議")
	}
	root, e := gateway.Endpoint(p.Endpoint)
	if e != nil {
		return e
	}
	p.Endpoint = root
	if strings.ContainsAny(p.Key, "\r\n") || len(p.Key) > 8192 {
		return errors.New("Key 含有無效字元")
	}
	seen := map[string]bool{}
	for i := range p.Models {
		m := &p.Models[i]
		m.ID = strings.TrimSpace(m.ID)
		if m.ID == "" || seen[m.ID] {
			return errors.New("模型 ID 不可留空或重複")
		}
		seen[m.ID] = true
		if m.Context < 0 {
			return errors.New("上下文長度不可為負數")
		}
		if m.Reasoning && len(m.Efforts) == 0 {
			m.Efforts = []string{"low", "medium", "high"}
		}
	}
	return nil
}
func (a *Service) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch method {
	case "initialize", "state.get":
		return a.state()
	case "profiles.save":
		var p domain.Profile
		if e := decode(raw, &p); e != nil {
			return nil, e
		}
		if e := validate(&p); e != nil {
			return nil, e
		}
		if e := a.Store.SaveProfile(p); e != nil {
			return nil, e
		}
		return a.state()
	case "profiles.delete":
		var p struct {
			ID string `json:"id"`
		}
		if e := decode(raw, &p); e != nil {
			return nil, e
		}
		for _, c := range []string{"claude", "codex"} {
			j, e := a.Config.Current(c)
			if e != nil {
				return nil, e
			}
			if j != nil && j.Binding.ProfileID == p.ID {
				return nil, errors.New("請先解除使用此連線的客戶端")
			}
		}
		if e := a.Store.DeleteProfile(p.ID); e != nil {
			return nil, e
		}
		return a.state()
	case "clients.apply":
		var b domain.Binding
		if e := decode(raw, &b); e != nil {
			return nil, e
		}
		if b.Mode != "direct" && b.Mode != "route" {
			return nil, errors.New("請選擇直連或本地路由")
		}
		if b.Client != "claude" && b.Client != "codex" {
			return nil, errors.New("無效客戶端")
		}
		p, e := a.Store.Profile(b.ProfileID)
		if e != nil {
			return nil, e
		}
		if !p.HasKey {
			return nil, errors.New("此連線尚未保存 API Key")
		}
		found := false
		for _, m := range p.Models {
			if m.ID == b.Model {
				found = true
			}
		}
		if !found {
			return nil, errors.New("請選擇此連線中的模型")
		}
		if b.Mode == "direct" && ((b.Client == "claude" && p.Protocol != domain.Messages) || (b.Client == "codex" && p.Protocol != domain.Responses)) {
			return nil, errors.New("此協議需要本地路由：Claude 直連使用 Messages，Codex 直連使用 Responses")
		}
		for _, id := range b.Roles {
			if id == "" {
				continue
			}
			ok := false
			for _, model := range p.Models {
				if model.ID == id {
					ok = true
				}
			}
			if !ok {
				return nil, errors.New("角色映射必須使用此連線中的模型")
			}
		}
		local := ""
		if b.Mode == "route" {
			local, e = a.Gateway.Start(a.settings.Port)
			if e != nil {
				return nil, e
			}
		}
		j, e := a.Config.Apply(b, p, local)
		if e != nil {
			if a.Gateway.RouteCount() == 0 {
				_ = a.stopGateway()
			}
			return nil, e
		}
		if b.Mode == "route" {
			a.Gateway.Set(gateway.Route{Binding: b, Profile: p, Token: j.Token})
		} else {
			a.Gateway.Remove(b.Client)
			if a.Gateway.RouteCount() == 0 {
				_ = a.stopGateway()
			}
		}
		if e = a.Store.Put("binding."+b.Client, b); e != nil {
			return nil, e
		}
		return a.state()
	case "clients.restore":
		var p struct {
			Client  string            `json:"client"`
			Choices map[string]string `json:"choices"`
		}
		if e := decode(raw, &p); e != nil {
			return nil, e
		}
		if p.Client != "claude" && p.Client != "codex" {
			return nil, errors.New("無效客戶端")
		}
		if _, e := a.Config.Restore(p.Client, p.Choices); e != nil {
			return nil, e
		}
		a.Gateway.Remove(p.Client)
		if a.Gateway.RouteCount() == 0 {
			_ = a.stopGateway()
		}
		return a.state()
	case "gateway.stop":
		if e := a.restoreRoutes(); e != nil {
			return nil, e
		}
		if e := a.stopGateway(); e != nil {
			return nil, e
		}
		return a.state()
	case "settings.save":
		var s domain.Settings
		if e := decode(raw, &s); e != nil {
			return nil, e
		}
		if s.Port < 0 || s.Port > 65535 {
			return nil, errors.New("埠號需介於 0 到 65535，0 表示動態埠")
		}
		if s.RetentionDays < 1 || s.RetentionDays > 3650 {
			return nil, errors.New("紀錄保留天數需介於 1 到 3650")
		}
		if s.Theme != "system" && s.Theme != "light" && s.Theme != "dark" {
			return nil, errors.New("無效主題")
		}
		running, _, _ := a.Gateway.Status()
		if running && s.Port != a.settings.Port {
			return nil, errors.New("請先停止本地路由再更換埠號")
		}
		if e := a.Store.Put("settings", s); e != nil {
			return nil, e
		}
		a.settings = s
		_ = a.Store.Prune(s.RetentionDays)
		return a.state()
	case "records.clear":
		if e := a.Store.ClearRecords(); e != nil {
			return nil, e
		}
		return a.state()
	case "models.fetch", "connection.test":
		var p struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Kind  string `json:"kind"`
		}
		if e := decode(raw, &p); e != nil {
			return nil, e
		}
		profile, e := a.Store.Profile(p.ID)
		if e != nil {
			return nil, e
		}
		if !profile.HasKey {
			return nil, errors.New("請先保存 API Key")
		}
		if method == "models.fetch" {
			return a.models(ctx, profile)
		}
		return a.test(ctx, profile, p.Model, p.Kind)
	case "backup.export":
		profiles, e := a.Store.Profiles()
		if e != nil {
			return nil, e
		}
		for i := range profiles {
			profiles[i].HasKey = false
		}
		bindings := []domain.Binding{}
		for _, c := range []string{"claude", "codex"} {
			var b domain.Binding
			if a.Store.Get("binding."+c, &b) == nil {
				bindings = append(bindings, b)
			}
		}
		return map[string]any{"format": "qc-switch", "version": 1, "profiles": profiles, "settings": a.settings, "bindings": bindings}, nil
	case "backup.import":
		var data struct {
			Format   string           `json:"format"`
			Version  int              `json:"version"`
			Profiles []domain.Profile `json:"profiles"`
			Settings *domain.Settings `json:"settings"`
			Bindings []domain.Binding `json:"bindings"`
		}
		if e := decode(raw, &data); e != nil {
			return nil, e
		}
		if data.Format != "qc-switch" || data.Version != 1 {
			return nil, errors.New("不支援的備份格式")
		}
		if data.Settings != nil {
			s := data.Settings
			if s.Port < 0 || s.Port > 65535 || s.RetentionDays < 1 || s.RetentionDays > 3650 || (s.Theme != "system" && s.Theme != "light" && s.Theme != "dark") {
				return nil, errors.New("備份中的偏好設定無效")
			}
			if running, _, _ := a.Gateway.Status(); running && s.Port != a.settings.Port {
				return nil, errors.New("請先停止本地路由再匯入不同埠號的備份")
			}
		}
		for _, b := range data.Bindings {
			if b.Client != "claude" && b.Client != "codex" {
				return nil, errors.New("備份中的客戶端無效")
			}
			if j, e := a.Config.Current(b.Client); e != nil {
				return nil, e
			} else if j != nil {
				return nil, errors.New("請先解除客戶端接管再匯入備份")
			}
		}
		for i := range data.Profiles {
			data.Profiles[i].Key = ""
			if e := validate(&data.Profiles[i]); e != nil {
				return nil, e
			}
		}
		for _, p := range data.Profiles {
			if e := a.Store.SaveProfile(p); e != nil {
				return nil, e
			}
		}
		if data.Settings != nil {
			if e := a.Store.Put("settings", *data.Settings); e != nil {
				return nil, e
			}
			a.settings = *data.Settings
		}
		// Restore selection only. Import never writes client files or starts routing.
		for _, b := range data.Bindings {
			if e := a.Store.Put("binding."+b.Client, b); e != nil {
				return nil, e
			}
		}
		return a.state()
	case "shutdown":
		for _, c := range []string{"claude", "codex"} {
			if _, e := a.Config.Restore(c, nil); e != nil {
				return nil, e
			}
			a.Gateway.Remove(c)
		}
		if e := a.stopGateway(); e != nil {
			return nil, e
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}
func (a *Service) stopGateway() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.Gateway.Stop(ctx)
}
func (a *Service) restoreRoutes() error {
	for _, c := range []string{"claude", "codex"} {
		j, e := a.Config.Current(c)
		if e != nil {
			return e
		}
		if j != nil && j.Binding.Mode == "route" {
			if _, e = a.Config.Restore(c, nil); e != nil {
				return e
			}
			a.Gateway.Remove(c)
		}
	}
	return nil
}
func (a *Service) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range []string{"claude", "codex"} {
		conflicts, e := a.Config.Restore(c, nil)
		if e != nil && len(conflicts) > 0 {
			choices := map[string]string{}
			for _, v := range conflicts {
				choices[v.Field] = "current"
			}
			_, _ = a.Config.Restore(c, choices)
		}
	}
	_ = a.stopGateway()
	_ = a.Store.Close()
}

func (a *Service) models(ctx context.Context, p domain.Profile) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", p.Endpoint+"/v1/models", nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+p.Key)
	resp, e := a.Gateway.Client.Do(req)
	if e != nil {
		return nil, errors.New("無法取得模型清單；請檢查端點與網路")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("模型清單 HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); e != nil {
		return nil, errors.New("模型清單格式不相容；可手動輸入模型")
	}
	return body.Data, nil
}

func (a *Service) test(ctx context.Context, p domain.Profile, model, kind string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if kind == "auth" {
		_, e := a.models(ctx, p)
		if e != nil {
			return map[string]any{"kind": kind, "ok": false, "message": e.Error()}, nil
		}
		return map[string]any{"kind": kind, "ok": true, "message": "模型清單認證成功；不代表所有模型皆有權限"}, nil
	}
	if kind != "text" && kind != "stream" && kind != "tools" {
		return nil, errors.New("無效測試類型")
	}
	found := false
	for _, m := range p.Models {
		if m.ID == model {
			found = true
		}
	}
	if !found {
		return nil, errors.New("請選擇已保存的模型")
	}
	r := protocol.Request{Model: model, Stream: kind == "stream", Messages: []protocol.Message{{Role: "user", Parts: []protocol.Part{{Kind: "text", Text: "Reply exactly QC_SWITCH_OK."}}}}, MaxTokens: 1024, Extras: protocol.Object{}}
	if kind == "tools" {
		r.Messages[0].Parts[0].Text = "Call qc_switch_probe with value QC_SWITCH_OK. Do not answer with text."
		r.Tools = []protocol.Tool{{Name: "qc_switch_probe", WireName: "qc_switch_probe", Kind: "function", Description: "A harmless connection test; no action is executed.", Schema: protocol.Object{"type": "object", "properties": protocol.Object{"value": protocol.Object{"type": "string"}}, "required": []string{"value"}}}}
		r.ToolChoice = "required"
	}
	body, toolctx, e := protocol.Encode(p.Protocol, r)
	if e != nil {
		return nil, e
	}
	data, _ := json.Marshal(body)
	req, e := http.NewRequestWithContext(ctx, "POST", p.Endpoint+gateway.Path(p.Protocol), bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.Key)
	if p.Protocol == domain.Messages {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, e := a.Gateway.Client.Do(req)
	if e != nil {
		return map[string]any{"kind": kind, "ok": false, "message": "連線失敗；請檢查端點、TLS 與網路"}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return map[string]any{"kind": kind, "ok": false, "message": fmt.Sprintf("上游 HTTP %d", resp.StatusCode)}, nil
	}
	var result protocol.Result
	if r.Stream {
		stream := protocol.NewStream(domain.Responses, model, "probe", toolctx, func(string, protocol.Object) error { return nil })
		e = stream.Run(p.Protocol, resp.Body)
		result = stream.Result
	} else {
		var obj protocol.Object
		e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&obj)
		if e == nil {
			result, e = protocol.ParseResult(p.Protocol, obj)
		}
	}
	if e != nil {
		return map[string]any{"kind": kind, "ok": false, "message": "回應不完整或協議不相容：" + e.Error()}, nil
	}
	ok := false
	if kind == "tools" {
		for _, part := range result.Parts {
			if part.Kind == "call" && part.Name == "qc_switch_probe" {
				var args protocol.Object
				if json.Unmarshal([]byte(part.Args), &args) == nil && args["value"] == "QC_SWITCH_OK" {
					ok = true
				}
			}
		}
	} else {
		ok = strings.Contains(protocol.Text(result.Parts), "QC_SWITCH_OK")
	}
	message := "測試完成；收到預期回應"
	if !ok {
		message = "請求成功，但未收到預期的測試文字或工具呼叫"
	}
	return map[string]any{"kind": kind, "ok": ok, "message": message, "inputTokens": result.Input, "outputTokens": result.Output}, nil
}

// Used by the portable launcher. No listener or user-level installation is needed.
func DefaultDirectory() string {
	exe, e := os.Executable()
	if e != nil {
		return "user-data"
	}
	return filepath.Join(filepath.Dir(exe), "user-data")
}
