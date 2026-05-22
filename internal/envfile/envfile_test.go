package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateValuesPreservesAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# comment\nAUTO_RESET_ENABLED=false\nNAME=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := UpdateValues(path, map[string]string{
		"AUTO_RESET_ENABLED":           "true",
		"MANUAL_CONFIRM_SUCCESS_COUNT": "3",
		"VALUE_WITH_SPACE":             "hello world",
	}); err != nil {
		t.Fatalf("UpdateValues() error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"# comment",
		"AUTO_RESET_ENABLED=true",
		"NAME=value",
		"MANUAL_CONFIRM_SUCCESS_COUNT=3",
		`VALUE_WITH_SPACE="hello world"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("updated .env missing %q:\n%s", want, text)
		}
	}
}
