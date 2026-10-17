//go:build windows

package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"qc-switch/internal/domain"
)

func TestSecretsEncryptedAndNeverListed(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	secret := "sk-private-test-secret-unique"
	p := domain.Profile{ID: "test", Name: "name", Key: secret}
	if e = s.SaveProfile(p); e != nil {
		t.Fatal(e)
	}
	p.Key = ""
	p.Name = "updated"
	if e = s.SaveProfile(p); e != nil {
		t.Fatal(e)
	}
	list, e := s.Profiles()
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(list)
	if bytes.Contains(b, []byte(secret)) || !list[0].HasKey {
		t.Fatal(string(b))
	}
	full, e := s.Profile("test")
	if e != nil || full.Key != secret {
		t.Fatal(e)
	}
	if e = s.SaveJournal("codex", map[string]any{"original": secret}); e != nil {
		t.Fatal(e)
	}
	var j map[string]any
	if e = s.Journal("codex", &j); e != nil || j["original"] != secret {
		t.Fatal(e)
	}
	s.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "qc-switch.db*"))
	for _, f := range files {
		data, _ := os.ReadFile(f)
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("plaintext credential on disk", f)
		}
	}
}
