package proxyenv

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestNormalizeMergesOnlyExplicitValuesAndDeduplicates(t *testing.T) {
	got, err := Normalize(
		"localhost, EXAMPLE.invalid",
		"example.invalid\n127.0.0.1",
		".service.invalid,LOCALHOST",
	)
	if err != nil {
		t.Fatal(err)
	}
	const want = "localhost,EXAMPLE.invalid,127.0.0.1,.service.invalid"
	if got != want {
		t.Fatalf("Normalize = %q, want %q", got, want)
	}
	summary := Summarize(got)
	if !summary.Configured || summary.EntryCount != 4 || len(summary.SHA256) != 64 {
		t.Fatalf("summary = %#v", summary)
	}
	if !Same(summary, Summarize(want)) {
		t.Fatal("matching normalized identities were not equal")
	}
}

func TestCurrentUsesUpperLowerAndManagedSpellings(t *testing.T) {
	t.Setenv("NO_PROXY", "upper.invalid")
	t.Setenv("no_proxy", "lower.invalid")
	t.Setenv(ManagedVariable, "managed.invalid")
	got, err := Current()
	if err != nil {
		t.Fatal(err)
	}
	if got != "upper.invalid,lower.invalid,managed.invalid" {
		t.Fatalf("Current = %q", got)
	}
}

func TestApplyPromotesCanonicalValueToHubAndChildSpellings(t *testing.T) {
	t.Setenv("NO_PROXY", "upper.invalid,shared.invalid")
	t.Setenv("no_proxy", "SHARED.invalid,lower.invalid")
	t.Setenv(ManagedVariable, "managed.invalid")
	canonical, err := Apply()
	if err != nil {
		t.Fatal(err)
	}
	const want = "upper.invalid,shared.invalid,lower.invalid,managed.invalid"
	if canonical != want {
		t.Fatalf("Apply = %q, want %q", canonical, want)
	}
	for _, name := range []string{"NO_PROXY", "no_proxy", ManagedVariable} {
		if got := os.Getenv(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestNormalizeFailsClosedWithoutEchoingInvalidValue(t *testing.T) {
	secretMarker := "do-not-echo"
	_, err := Normalize(secretMarker + "\x00.invalid")
	if err == nil {
		t.Fatal("Normalize accepted a NUL byte")
	}
	if strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("error leaked input: %v", err)
	}

	values := make([]string, maxEntries+1)
	for i := range values {
		values[i] = fmt.Sprintf("host-%04d.invalid", i)
	}
	_, err = Normalize(strings.Join(values, ","))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("entry limit error = %v", err)
	}
}

func TestEmptySummaryDoesNotPublishEmptyDigest(t *testing.T) {
	summary := Summarize("")
	if summary.Configured || summary.EntryCount != 0 || summary.SHA256 != "" {
		t.Fatalf("empty summary = %#v", summary)
	}
}
