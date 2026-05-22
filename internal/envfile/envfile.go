package envfile

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Load reads a simple KEY=value .env file. Missing files return an empty map.
func Load(path string) (map[string]string, error) {
	values := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return values, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseLine(scanner.Text())
		if ok {
			values[key] = value
		}
	}
	return values, scanner.Err()
}

// UpdateValues updates or appends keys while preserving unrelated lines.
func UpdateValues(path string, updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}

	input, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	seen := make(map[string]bool, len(updates))
	lines := splitLines(string(input))
	for i, line := range lines {
		key, _, ok := parseLine(line)
		if !ok {
			continue
		}
		value, exists := updates[key]
		if !exists {
			continue
		}
		lines[i] = fmt.Sprintf("%s=%s", key, FormatValue(value))
		seen[key] = true
	}

	var missing []string
	for key := range updates {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		lines = append(lines, fmt.Sprintf("%s=%s", key, FormatValue(updates[key])))
	}

	var out bytes.Buffer
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".env-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err == nil {
		return nil
	}

	// Windows can reject Rename over an existing file. Fall back to replace.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, path)
}

func parseLine(line string) (string, string, bool) {
	line = strings.TrimPrefix(line, "\ufeff")
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}
	idx := strings.IndexByte(trimmed, '=')
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(trimmed[:idx])
	if !validKey(key) {
		return "", "", false
	}
	value := strings.TrimSpace(trimmed[idx+1:])
	return key, unquote(value), true
}

func validKey(key string) bool {
	for i, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return key != ""
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	quote := value[0]
	if quote != '\'' && quote != '"' || value[len(value)-1] != quote {
		return value
	}
	value = value[1 : len(value)-1]
	if quote == '\'' {
		return value
	}
	replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`)
	return replacer.Replace(value)
}

// FormatValue writes a value in a conservative .env-safe form.
func FormatValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\r\n#'\"\\") {
		value = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(value)
		return `"` + value + `"`
	}
	return value
}

func splitLines(input string) []string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.TrimSuffix(input, "\n")
	if input == "" {
		return nil
	}
	return strings.Split(input, "\n")
}
