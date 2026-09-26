package update

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParseVersion parses versions like "v2.0.0.0" or "2.0.0".
// Missing components are treated as zero up to 4 segments.
func ParseVersion(raw string) ([4]int, error) {
	var out [4]int

	normalized := strings.TrimSpace(raw)
	if normalized == "" {
		return out, fmt.Errorf("empty version")
	}
	normalized = strings.TrimPrefix(strings.TrimPrefix(normalized, "v"), "V")

	for _, sep := range []string{"-", "+"} {
		if idx := strings.Index(normalized, sep); idx >= 0 {
			normalized = normalized[:idx]
		}
	}
	parts := strings.Split(normalized, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return out, fmt.Errorf("unsupported version format: %s", raw)
	}

	for i := 0; i < len(parts) && i < 4; i++ {
		token := strings.TrimSpace(parts[i])
		if token == "" {
			return out, fmt.Errorf("invalid empty version segment: %s", raw)
		}
		token = leadingDigits(token)
		if token == "" {
			return out, fmt.Errorf("invalid version segment: %s", parts[i])
		}

		n, err := strconv.Atoi(token)
		if err != nil {
			return out, fmt.Errorf("parse version segment %q: %w", token, err)
		}
		if n < 0 {
			return out, fmt.Errorf("negative version segment: %d", n)
		}
		out[i] = n
	}
	return out, nil
}

// CompareVersions compares two semantic-like versions.
// Returns -1 when a < b, 0 when equal, 1 when a > b.
func CompareVersions(a, b string) (int, error) {
	av, err := ParseVersion(a)
	if err != nil {
		return 0, fmt.Errorf("parse version a: %w", err)
	}
	bv, err := ParseVersion(b)
	if err != nil {
		return 0, fmt.Errorf("parse version b: %w", err)
	}

	for i := 0; i < 4; i++ {
		if av[i] < bv[i] {
			return -1, nil
		}
		if av[i] > bv[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func leadingDigits(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if !unicode.IsDigit(r) {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}
