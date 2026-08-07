package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func randomTestCredential(t *testing.T) string {
	t.Helper()
	buffer := make([]byte, 48)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatal("random test credential generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}
