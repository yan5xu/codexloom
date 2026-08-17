// Package proxyenv owns CodexLoom's explicit proxy-bypass contract.
//
// It intentionally does not inspect Provider configuration or derive hosts
// from URLs. Only values supplied by the operator through NO_PROXY, no_proxy,
// and CODEX_LOOM_NO_PROXY participate in the managed value.
package proxyenv

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const (
	ManagedVariable = "CODEX_LOOM_NO_PROXY"
	maxEntries      = 1024
	maxCanonicalLen = 16 << 10
)

// Summary is safe to expose through logs and diagnostics: it proves identity
// without revealing the operator's host/domain list.
type Summary struct {
	Configured bool   `json:"configured"`
	EntryCount int    `json:"entryCount"`
	SHA256     string `json:"sha256,omitempty"`
}

// Current normalizes the three explicit operator-controlled process values.
func Current() (string, error) {
	return Normalize(
		os.Getenv("NO_PROXY"),
		os.Getenv("no_proxy"),
		os.Getenv(ManagedVariable),
	)
}

// Apply promotes the managed value to the standard upper- and lower-case
// variables before the Hub creates any HTTP clients. This is the point that
// makes CODEX_LOOM_NO_PROXY effective for Hub requests as well as child
// processes. Call it at process startup, before http.ProxyFromEnvironment can
// cache its first result.
func Apply() (string, error) {
	canonical, err := Current()
	if err != nil {
		return "", err
	}
	if canonical == "" {
		return "", nil
	}
	for _, name := range []string{"NO_PROXY", "no_proxy", ManagedVariable} {
		if err := os.Setenv(name, canonical); err != nil {
			return "", fmt.Errorf("apply normalized proxy bypass environment: %w", err)
		}
	}
	return canonical, nil
}

// Normalize merges comma- or whitespace-separated values, removes empty
// entries, and de-duplicates case-insensitively while retaining the first
// spelling and order. It never parses URLs or Provider configuration.
func Normalize(values ...string) (string, error) {
	seen := map[string]struct{}{}
	entries := make([]string, 0)
	for _, value := range values {
		if strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("proxy bypass configuration contains an invalid control character")
		}
		for _, entry := range strings.FieldsFunc(value, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			for _, r := range entry {
				if unicode.IsControl(r) {
					return "", fmt.Errorf("proxy bypass configuration contains an invalid control character")
				}
			}
			key := strings.ToLower(entry)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			entries = append(entries, entry)
			if len(entries) > maxEntries {
				return "", fmt.Errorf("proxy bypass configuration exceeds %d entries", maxEntries)
			}
		}
	}
	canonical := strings.Join(entries, ",")
	if len(canonical) > maxCanonicalLen {
		return "", fmt.Errorf("proxy bypass configuration exceeds %d bytes", maxCanonicalLen)
	}
	return canonical, nil
}

// Summarize returns a non-reversible operational identity for a canonical
// value. Callers should Normalize first.
func Summarize(canonical string) Summary {
	if canonical == "" {
		return Summary{}
	}
	digest := sha256.Sum256([]byte(canonical))
	return Summary{
		Configured: true,
		EntryCount: strings.Count(canonical, ",") + 1,
		SHA256:     hex.EncodeToString(digest[:]),
	}
}

// Same reports exact normalized identity without exposing either value.
func Same(left, right Summary) bool {
	return left.Configured == right.Configured &&
		left.EntryCount == right.EntryCount &&
		left.SHA256 == right.SHA256
}
