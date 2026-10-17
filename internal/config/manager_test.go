//go:build windows

package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qc-switch/internal/domain"
	"qc-switch/internal/store"
)

func manager(t *testing.T) *Manager {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return &Manager{Store: s}
}
func fixtureProfile() domain.Profile {
	return domain.Profile{ID: "test", Name: "test", Endpoint: "https://quickrouter.us", Key: "secret-test-key", Protocol: domain.Responses, Models: []domain.Model{{ID: "model", Tools: true}}}
}

func TestTakeoverRestoreAndExternalChanges(t *testing.T) {
	for _, client := range []string{"claude", "codex"} {
		t.Run(client, func(t *testing.T) {
			m := manager(t)
			dir := t.TempDir()
			path, _ := Path(client, dir)
			original := []byte("# retained comment\nmodel = \"original\" # model comment\n[other]\nvalues = [1, 2, 3]\n")
			if client == "claude" {
				original = []byte("{\"model\":\"original\",\"env\":{\"ANTHROPIC_API_KEY\":\"old-secret\"},\"permissions\":{\"allow\":[\"Read\"]}}\n")
			}
			if e := os.WriteFile(path, original, 0600); e != nil {
				t.Fatal(e)
			}
			login := filepath.Join(dir, "auth.json")
			os.WriteFile(login, []byte("untouched-login"), 0600)
			b := domain.Binding{Client: client, Directory: dir, Mode: "route", Model: "model", ProfileID: "test"}
			j, e := m.Apply(b, fixtureProfile(), "http://127.0.0.1:15722")
			if e != nil {
				t.Fatal(e)
			}
			if j.Token == "" || bytes.Contains(j.Applied, []byte("secret-test-key")) {
				t.Fatal("upstream secret leaked")
			}
			if client == "codex" && !bytes.Contains(j.Applied, []byte("# retained comment")) {
				t.Fatal("comment lost")
			}
			if _, e = m.Restore(client, nil); e != nil {
				t.Fatal(e)
			}
			restored, _ := os.ReadFile(path)
			if !bytes.Equal(restored, original) {
				t.Fatalf("not exact restoration: %s", restored)
			}
			j, e = m.Apply(b, fixtureProfile(), "http://127.0.0.1:15722")
			if e != nil {
				t.Fatal(e)
			}
			external, e := apply(client, j.Applied, map[string]any{"model": "external", "unrelated": "keep-me"})
			if e != nil {
				t.Fatal(e)
			}
			os.WriteFile(path, external, 0600)
			_, e = m.Restore(client, nil)
			var conflict *ConflictError
			if !errors.As(e, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Field != "model" {
				t.Fatal(e, conflict)
			}
			if _, e = m.Restore(client, map[string]string{"model": "current"}); e != nil {
				t.Fatal(e)
			}
			data, _ := os.ReadFile(path)
			parsed, e := parse(client, data)
			if e != nil {
				t.Fatal(e)
			}
			if parsed["model"] != "external" || parsed["unrelated"] != "keep-me" {
				t.Fatal(string(data))
			}
			auth, _ := os.ReadFile(login)
			if string(auth) != "untouched-login" {
				t.Fatal("login overwritten")
			}
		})
	}
}

func TestCustomEnvironmentDirectoriesAndCrashRecovery(t *testing.T) {
	m := manager(t)
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	path, e := Path("codex", "")
	if e != nil || path != filepath.Join(dir, "config.toml") {
		t.Fatal(path, e)
	}
	b := domain.Binding{Client: "codex", Model: "model", Mode: "route"}
	j, e := m.Apply(b, fixtureProfile(), "http://127.0.0.1:1234")
	if e != nil {
		t.Fatal(e)
	}
	j.Pending = true
	m.Store.SaveJournal("codex", j)
	if messages := m.Recover(); len(messages) != 1 {
		t.Fatal(messages)
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("new config not removed", e)
	}
	// A crash after journal creation but before writing must leave original intact.
	original := []byte("model = \"old\"\n")
	os.WriteFile(path, original, 0600)
	m.Store.SaveJournal("codex", &Journal{Path: path, Original: original, Applied: []byte("model = \"new\"\n"), Existed: true, Pending: true})
	m.Recover()
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal(string(data))
	}
}

func TestAtomicWriteFailureAndConflictingReapply(t *testing.T) {
	m := manager(t)
	dir := t.TempDir()
	path, _ := Path("codex", dir)
	os.WriteFile(path, []byte("model = \"old\"\n"), 0600)
	b := domain.Binding{Client: "codex", Directory: dir, Model: "model", Mode: "route"}
	_, e := m.Apply(b, fixtureProfile(), "http://127.0.0.1:1")
	if e != nil {
		t.Fatal(e)
	}
	before, _ := m.Current("codex")
	data, _ := os.ReadFile(path)
	data, e = PatchTOML(data, map[string]any{"model": "external"})
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(path, data, 0600)
	b.Model = "new"
	if _, e = m.Apply(b, fixtureProfile(), "http://127.0.0.1:2"); e == nil {
		t.Fatal("overwrote external changes")
	}
	after, _ := m.Current("codex")
	if string(before.Applied) != string(after.Applied) || before.Fields["model"].After != after.Fields["model"].After {
		t.Fatal("failed reapply changed journal")
	}
	locked := filepath.Join(t.TempDir(), "directory")
	os.Mkdir(locked, 0700)
	if AtomicWrite(locked, []byte("x")) == nil {
		t.Fatal("directory unexpectedly replaced")
	}
	if !strings.Contains(string(data), "external") {
		t.Fatal(string(data))
	}
}
