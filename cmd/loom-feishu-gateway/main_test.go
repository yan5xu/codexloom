package main

import "testing"

func TestExplicitFeishuCredentialReferenceOverridesAmbientSecret(t *testing.T) {
	t.Setenv("FEISHU_APP_SECRET", "ambient-test-value")
	t.Setenv("FEISHU_EXPLICIT_TEST_SECRET", "explicit-test-value")

	secret, err := resolveFeishuGatewaySecret("app-test", "env:FEISHU_EXPLICIT_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if secret != "explicit-test-value" {
		t.Fatalf("resolved secret source = %q, want explicit reference", secret)
	}
}

func TestInvalidExplicitFeishuCredentialReferenceDoesNotFallBackToAmbientSecret(t *testing.T) {
	t.Setenv("FEISHU_APP_SECRET", "ambient-test-value")

	if secret, err := resolveFeishuGatewaySecret("app-test", "unsupported:test"); err == nil || secret != "" {
		t.Fatalf("invalid explicit reference fell back to ambient secret: secret=%q err=%v", secret, err)
	}
}
