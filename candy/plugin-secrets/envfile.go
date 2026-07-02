package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// envfile.go carries the ONE env-file parser the externalized `secrets gpg env`/`decrypt`
// surface needs (ParseEnvBytes), ported out of charly's core envfile.go. The plugin is a
// separate Go module, so it cannot import charly's package-main ParseEnvBytes; this is the
// module-boundary copy (the two modules each own an env parser, not in-module duplication).

// ParseEnvBytes parses KEY=VALUE entries from raw bytes.
// Skips comments (#), blank lines, and strips surrounding quotes from values.
func ParseEnvBytes(data []byte) ([]string, error) {
	var envs []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Must contain = for KEY=VALUE format
		before, after, ok := strings.Cut(line, "=")
		if !ok {
			// KEY without value — pass through as-is (docker behavior: inherits from host)
			envs = append(envs, line)
			continue
		}

		key := before
		value := after

		// Strip surrounding quotes from value
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		envs = append(envs, key+"="+value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing env data: %w", err)
	}

	return envs, nil
}
