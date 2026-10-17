//go:build windows

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qc-switch/internal/domain"
)

func call(t *testing.T, a *Service, m string, p any) any {
	t.Helper()
	b, _ := json.Marshal(p)
	v, e := a.Handle(context.Background(), m, b)
	if e != nil {
		t.Fatal(m, e)
	}
	return v
}
func TestNoListenerUntilRouteAndBackupWithoutSecrets(t *testing.T) {
	a, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"m"}]}`))
			return
		}
		w.Write([]byte(`{"id":"r","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"QC_SWITCH_OK"}]}]}`))
	}))
	defer up.Close()
	state := call(t, a, "initialize", nil).(domain.State)
	if state.Running {
		t.Fatal("listener started at launch")
	}
	call(t, a, "profiles.save", domain.Profile{ID: "p", Name: "p", Endpoint: up.URL + "/v1", Protocol: domain.Responses, Key: "sk-local-test", Models: []domain.Model{{ID: "m", Tools: true}}})
	call(t, a, "models.fetch", map[string]any{"id": "p"})
	call(t, a, "connection.test", map[string]any{"id": "p", "model": "m", "kind": "text"})
	if running, _, _ := a.Gateway.Status(); running {
		t.Fatal("probe started listener")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("original-auth"), 0600)
	b := domain.Binding{Client: "codex", ProfileID: "p", Model: "m", Mode: "direct", Directory: dir}
	call(t, a, "clients.apply", b)
	if running, _, _ := a.Gateway.Status(); running {
		t.Fatal("direct mode started listener")
	}
	backup := call(t, a, "backup.export", nil)
	encoded, _ := json.Marshal(backup)
	if strings.Contains(string(encoded), "sk-local-test") {
		t.Fatal("backup contains credential")
	}
	call(t, a, "clients.restore", map[string]any{"client": "codex"})
	call(t, a, "backup.import", backup)
	if _, e = os.Stat(filepath.Join(dir, "config.toml")); !os.IsNotExist(e) {
		t.Fatal("import activated takeover")
	}
	b.Mode = "route"
	call(t, a, "settings.save", domain.Settings{Port: 0, Theme: "system", RetentionDays: 30})
	call(t, a, "clients.apply", b)
	if running, address, _ := a.Gateway.Status(); !running || !strings.HasPrefix(address, "http://127.0.0.1:") {
		t.Fatal("route listener missing", address)
	}
	call(t, a, "shutdown", nil)
	if running, _, _ := a.Gateway.Status(); running {
		t.Fatal("listener still running")
	}
	auth, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if string(auth) != "original-auth" {
		t.Fatal("auth changed")
	}
}

func TestCorrectAPIEndpointKeepsStoredKeyAndModels(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	profile := domain.Profile{ID: "endpoint-fix", Name: "existing", Endpoint: "https://quickrouter.ai", Protocol: domain.Responses, Key: "isolated-test-key", Models: []domain.Model{{ID: "chosen-model", Tools: true}}}
	call(t, a, "profiles.save", profile)
	profile.Endpoint = "https://api.quickrouter.ai/v1/"
	profile.Key = "" // UI edits and the one-click correction omit the saved key.
	state := call(t, a, "profiles.save", profile).(domain.State)
	if len(state.Profiles) != 1 || state.Profiles[0].Endpoint != "https://api.quickrouter.ai" || !state.Profiles[0].HasKey || len(state.Profiles[0].Models) != 1 {
		t.Fatalf("endpoint correction lost existing settings: %#v", state.Profiles)
	}
	stored, err := a.Store.Profile(profile.ID)
	if err != nil || stored.Key != "isolated-test-key" || stored.Models[0].ID != "chosen-model" {
		t.Fatal("endpoint edit lost encrypted credential or model", err)
	}
	if running, _, _ := a.Gateway.Status(); running {
		t.Fatal("endpoint edit started a listener")
	}
}
