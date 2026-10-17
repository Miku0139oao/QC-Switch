package config

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"qc-switch/internal/domain"
	"qc-switch/internal/store"
)

type Field struct {
	Before  any  `json:"before"`
	Existed bool `json:"existed"`
	After   any  `json:"after"`
}
type Journal struct {
	Path     string           `json:"path"`
	Existed  bool             `json:"existed"`
	Original []byte           `json:"original"`
	Applied  []byte           `json:"applied"`
	Fields   map[string]Field `json:"fields"`
	Binding  domain.Binding   `json:"binding"`
	Token    string           `json:"token"`
	Pending  bool             `json:"pending"`
}
type Conflict struct {
	Field    string `json:"field"`
	Current  any    `json:"current"`
	Original any    `json:"original"`
	Managed  any    `json:"managed"`
}
type ConflictError struct {
	Conflicts []Conflict `json:"conflicts"`
}

func (e *ConflictError) Error() string {
	return "設定已被其他程式修改；請選擇保留目前值或還原備份"
}

type Manager struct {
	Store *store.Store
	mu    sync.Mutex
}

func AtomicWrite(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".qc-switch-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return replaceFile(name, path)
}
func ID() string {
	b := make([]byte, 16)
	_, e := rand.Read(b)
	if e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Path(client, dir string) (string, error) {
	if client != "claude" && client != "codex" {
		return "", errors.New("unknown client")
	}
	if dir == "" {
		if client == "claude" {
			dir = os.Getenv("CLAUDE_CONFIG_DIR")
		} else {
			dir = os.Getenv("CODEX_HOME")
		}
	}
	if dir == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "", e
		}
		if client == "claude" {
			dir = filepath.Join(home, ".claude")
		} else {
			dir = filepath.Join(home, ".codex")
		}
	}
	abs, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	file := "config.toml"
	if client == "claude" {
		file = "settings.json"
	}
	return filepath.Join(abs, file), nil
}
func get(m map[string]any, path string) (any, bool) {
	var v any = m
	for _, k := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		var found bool
		v, found = obj[k]
		if !found {
			return nil, false
		}
	}
	return v, true
}
func set(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	cur := m
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	if v == nil {
		delete(cur, keys[len(keys)-1])
	} else {
		cur[keys[len(keys)-1]] = v
	}
}
func parse(client string, b []byte) (map[string]any, error) {
	if client == "codex" {
		return readTOML(b)
	}
	m := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return m, nil
	}
	e := json.Unmarshal(b, &m)
	if m == nil {
		return nil, errors.New("settings must be a JSON object")
	}
	return m, e
}
func apply(client string, b []byte, changes map[string]any) ([]byte, error) {
	if client == "codex" {
		return PatchTOML(b, changes)
	}
	m, e := parse(client, b)
	if e != nil {
		return nil, e
	}
	for k, v := range changes {
		set(m, k, v)
	}
	out, e := json.MarshalIndent(m, "", "  ")
	return append(out, '\n'), e
}

func (m *Manager) Current(client string) (*Journal, error) {
	var j Journal
	e := m.Store.Journal(client, &j)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &j, e
}
func (m *Manager) Apply(b domain.Binding, p domain.Profile, local string) (*Journal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, e := Path(b.Client, b.Directory)
	if e != nil {
		return nil, e
	}
	old, e := m.Current(b.Client)
	if e != nil {
		return nil, e
	}
	if old != nil && old.Path != path {
		return nil, errors.New("請先還原目前設定，再更換設定目錄")
	}
	current, e := os.ReadFile(path)
	existed := e == nil
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	parsed, e := parse(b.Client, current)
	if e != nil {
		return nil, e
	}
	j := &Journal{Path: path, Existed: existed, Original: current, Fields: map[string]Field{}, Token: ID(), Binding: b, Pending: true}
	if old != nil {
		j.Existed = old.Existed
		j.Original = old.Original
		for k, v := range old.Fields {
			j.Fields[k] = v
		}
		j.Token = old.Token
	}
	changes := map[string]any{}
	root := strings.TrimSuffix(strings.TrimSuffix(p.Endpoint, "/"), "/v1")
	key := p.Key
	if b.Mode == "route" {
		root = local
		key = j.Token
	}
	if b.Client == "claude" {
		changes["env.ANTHROPIC_BASE_URL"] = root
		changes["env.ANTHROPIC_AUTH_TOKEN"] = key
		changes["env.ANTHROPIC_API_KEY"] = nil
		changes["model"] = b.Model
		changes["env.ANTHROPIC_MODEL"] = b.Model
		for role, env := range map[string]string{"sonnet": "ANTHROPIC_DEFAULT_SONNET_MODEL", "opus": "ANTHROPIC_DEFAULT_OPUS_MODEL", "haiku": "ANTHROPIC_DEFAULT_HAIKU_MODEL", "subagent": "CLAUDE_CODE_SUBAGENT_MODEL"} {
			model := b.Roles[role]
			if model == "" {
				model = b.Model
			}
			changes["env."+env] = model
		}
	} else {
		changes["model_provider"] = "qc_switch"
		changes["model"] = b.Model
		changes["model_providers.qc_switch.name"] = "QC Switch"
		changes["model_providers.qc_switch.base_url"] = root + "/v1"
		changes["model_providers.qc_switch.wire_api"] = "responses"
		changes["model_providers.qc_switch.experimental_bearer_token"] = key
		changes["model_providers.qc_switch.requires_openai_auth"] = false
		changes["model_providers.qc_switch.supports_websockets"] = false
		changes["web_search"] = "disabled"
		catalog, e := m.catalog(p)
		if e != nil {
			return nil, e
		}
		changes["model_catalog_json"] = catalog
	}
	// Refuse to overwrite fields changed externally during an existing takeover.
	conflicts := []Conflict{}
	for k, v := range changes {
		cur, has := get(parsed, k)
		field, owned := j.Fields[k]
		if owned && !reflect.DeepEqual(cur, field.After) && !(field.After == nil && !has) {
			conflicts = append(conflicts, Conflict{Field: k, Current: redact(k, cur), Original: redact(k, field.Before), Managed: redact(k, field.After)})
		}
		if !owned {
			field = Field{Before: cur, Existed: has}
		}
		field.After = v
		j.Fields[k] = field
	}
	if len(conflicts) > 0 {
		return nil, &ConflictError{conflicts}
	}
	updated, e := apply(b.Client, current, changes)
	if e != nil {
		return nil, e
	}
	j.Applied = updated
	if e = m.Store.SaveJournal(b.Client, j); e != nil {
		return nil, e
	}
	if e = AtomicWrite(path, updated); e != nil {
		if old != nil {
			_ = m.Store.SaveJournal(b.Client, old)
		} else {
			_ = m.Store.DeleteJournal(b.Client)
		}
		return nil, e
	}
	j.Pending = false
	if e = m.Store.SaveJournal(b.Client, j); e != nil {
		return nil, e
	}
	return j, nil
}

func redact(k string, v any) any {
	low := strings.ToLower(k)
	if strings.Contains(low, "token") || strings.Contains(low, "key") {
		if v != nil {
			return "••••••••"
		}
	}
	return v
}

// choices maps conflicting field paths to "current" or "original". No blanket
// force flag can overwrite unrelated edits.
func (m *Manager) Restore(client string, choices map[string]string) ([]Conflict, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, e := m.Current(client)
	if e != nil || j == nil {
		return nil, e
	}
	current, e := os.ReadFile(j.Path)
	if os.IsNotExist(e) {
		return nil, m.Store.DeleteJournal(client)
	}
	if e != nil {
		return nil, e
	}
	if bytes.Equal(current, j.Original) {
		return nil, m.Store.DeleteJournal(client)
	}
	if bytes.Equal(current, j.Applied) {
		if j.Existed {
			e = AtomicWrite(j.Path, j.Original)
		} else {
			e = os.Remove(j.Path)
		}
		if e != nil {
			return nil, e
		}
		return nil, m.Store.DeleteJournal(client)
	}
	parsed, e := parse(client, current)
	if e != nil {
		return nil, e
	}
	changes := map[string]any{}
	conflicts := []Conflict{}
	for k, f := range j.Fields {
		v, has := get(parsed, k)
		matches := reflect.DeepEqual(v, f.After) || (f.After == nil && !has)
		originalMatches := reflect.DeepEqual(v, f.Before) && has == f.Existed
		if originalMatches {
			continue
		}
		if !matches {
			choice := choices[k]
			if choice == "current" {
				continue
			}
			if choice != "original" {
				conflicts = append(conflicts, Conflict{Field: k, Current: redact(k, v), Original: redact(k, f.Before), Managed: redact(k, f.After)})
				continue
			}
		}
		if f.Existed {
			changes[k] = f.Before
		} else {
			changes[k] = nil
		}
	}
	if len(conflicts) > 0 {
		return conflicts, &ConflictError{conflicts}
	}
	updated, e := apply(client, current, changes)
	if e != nil {
		return nil, e
	}
	if e = AtomicWrite(j.Path, updated); e != nil {
		return nil, e
	}
	return nil, m.Store.DeleteJournal(client)
}

func (m *Manager) Recover() []string {
	out := []string{}
	clients, e := m.Store.JournalClients()
	if e != nil {
		return []string{e.Error()}
	}
	for _, c := range clients {
		_, e = m.Restore(c, nil)
		if e != nil {
			out = append(out, c+": "+e.Error())
		} else {
			out = append(out, c+": 已恢復上次接管前的設定")
		}
	}
	return out
}

func (m *Manager) catalog(p domain.Profile) (string, error) {
	models := []any{}
	for i, model := range p.Models {
		efforts := []any{}
		for _, e := range model.Efforts {
			efforts = append(efforts, map[string]any{"effort": e, "description": e})
		}
		context := model.Context
		if context <= 0 {
			context = 32000
		}
		name := model.Name
		if name == "" {
			name = model.ID
		}
		var defaultEffort any
		if model.Reasoning && len(model.Efforts) > 0 {
			defaultEffort = model.Efforts[0]
		}
		// shell_command remains a supported alias in Codex 0.153; older clients
		// use that spelling. Both the legacy and current instruction fields are
		// supplied so a shared CLI/extension/desktop configuration can be read.
		instructions := "You are a coding assistant. Use available tools to complete the user's task accurately."
		models = append(models, map[string]any{"slug": model.ID, "display_name": name, "description": "QuickRouter · " + string(p.Protocol), "visibility": "list", "supported_in_api": true, "priority": i, "upgrade": nil, "availability_nux": nil, "context_window": context, "effective_context_window_percent": 95, "default_reasoning_level": defaultEffort, "supported_reasoning_levels": efforts, "supports_reasoning_summaries": false, "supports_reasoning_summary_parameter": false, "supports_parallel_tool_calls": model.Tools, "supports_search_tool": false, "experimental_supported_tools": []string{}, "support_verbosity": false, "default_verbosity": nil, "shell_type": "shell_command", "apply_patch_tool_type": "freeform", "input_modalities": modalities(model.Vision), "prefer_websockets": false, "truncation_policy": map[string]any{"mode": "tokens", "limit": 10000}, "base_instructions": instructions, "model_messages": map[string]any{"instructions_template": instructions}})
	}
	path := filepath.Join(m.Store.Dir, "catalog-"+p.ID+".json")
	b, e := json.MarshalIndent(map[string]any{"models": models}, "", "  ")
	if e != nil {
		return "", e
	}
	return path, AtomicWrite(path, b)
}
func modalities(vision bool) []string {
	if vision {
		return []string{"text", "image"}
	}
	return []string{"text"}
}
func Diagnostics(client, dir string) domain.ClientState {
	path, e := Path(client, dir)
	s := domain.ClientState{Client: client, Path: path, Warnings: []string{}}
	if e != nil {
		s.Warnings = append(s.Warnings, e.Error())
		return s
	}
	if _, e = exec.LookPath(client); e != nil {
		s.Warnings = append(s.Warnings, "CLI 未出現在 PATH；仍可管理擴充套件／桌面客戶端設定")
	}
	keys := []string{"OPENAI_BASE_URL", "CODEX_HOME"}
	if client == "claude" {
		keys = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL", "CLAUDE_CONFIG_DIR"}
	}
	for _, k := range keys {
		if os.Getenv(k) != "" {
			s.Warnings = append(s.Warnings, k+" 已在目前程序環境設定；請檢查客戶端的實際設定優先順序")
		}
	}
	return s
}
