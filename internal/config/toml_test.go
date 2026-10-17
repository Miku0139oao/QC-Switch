package config

import (
	"strings"
	"testing"
)

func TestTOMLPreservesUnownedSource(t *testing.T) {
	original := "# header\nmodel = \"old\" # keep inline\nlist = [\n  1, # one\n  2,\n]\ninline = { a = 1, b = \"two\" }\ntext = '''multi\nline'''\n[model_providers.existing]\nbase_url = \"https://example.com/v1\" # existing\n[projects.\"D:\\\\code\"]\ntrust_level = \"trusted\"\n"
	out, e := PatchTOML([]byte(original), map[string]any{"model": "new", "model_provider": "qc_switch", "model_providers.qc_switch.wire_api": "responses", "model_providers.qc_switch.requires_openai_auth": false})
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{"# header", "# keep inline", "list = [\n  1, # one\n  2,\n]", "inline = { a = 1, b = \"two\" }", "text = '''multi\nline'''", "# existing", "trust_level = \"trusted\""} {
		if !strings.Contains(string(out), s) {
			t.Fatalf("lost %q: %s", s, out)
		}
	}
	m, e := readTOML(out)
	if e != nil {
		t.Fatal(e)
	}
	if m["model"] != "new" || m["model_provider"] != "qc_switch" {
		t.Fatal(string(out))
	}
}

func TestTOMLReplaceComplexAndNoFinalNewline(t *testing.T) {
	for _, src := range []string{"value = [1, 2, [3, 4]] # tail\nkeep = 9\n", "value = { a = 1, b = { c = 2 } } # tail\nkeep = 9\n", "value = '''abc\ndef''' # tail\nkeep = 9\n"} {
		out, e := PatchTOML([]byte(src), map[string]any{"value": "replaced"})
		if e != nil {
			t.Fatal(e)
		}
		m, e := readTOML(out)
		if e != nil || m["value"] != "replaced" || m["keep"] != int64(9) {
			t.Fatal(string(out), e)
		}
		if !strings.Contains(string(out), "# tail") {
			t.Fatal(string(out))
		}
	}
	out, e := PatchTOML([]byte("model = \"old\""), map[string]any{"new_key": "v"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = readTOML(out); e != nil {
		t.Fatal(e)
	}
}
