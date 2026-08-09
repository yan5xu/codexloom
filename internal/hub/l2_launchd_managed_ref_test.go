package hub

import (
	"strings"
	"testing"

	"github.com/yan5xu/codex-loom/internal/credentials"
)

func TestL2LaunchdUnitCarriesFrozenManagedRef(t *testing.T) {
	ref := "managed:" + strings.Repeat("a", 64)
	descriptor := r1Descriptor("conn_l2", "build", strings.Repeat("0", 64), "/tmp/l2-executable")
	descriptor.ManagedCredentialRef = ref
	launchdDescriptor := descriptor
	launchdDescriptor.Manager = gatewayServiceManagerLaunchd
	launchdDescriptor.UnitPath = "/tmp/l2.plist"
	launchd, err := renderGatewayServiceUnit(launchdDescriptor, "gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launchd), "CODEX_LOOM_MANAGED_CREDENTIAL_REF") || !strings.Contains(string(launchd), ref) {
		t.Fatalf("launchd unit does not carry the managed ref: %s", launchd)
	}
	systemdDescriptor := descriptor
	systemdDescriptor.Manager = gatewayServiceManagerSystemd
	systemdDescriptor.UnitPath = "/tmp/l2.service"
	systemd, err := renderGatewayServiceUnit(systemdDescriptor, "gen-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(systemd), "CODEX_LOOM_MANAGED_CREDENTIAL_REF=") {
		t.Fatalf("systemd unit does not carry the managed ref: %s", systemd)
	}
	// The rendered unit is canonical: identical render for identical descriptor.
	again, err := renderGatewayServiceUnit(launchdDescriptor, "gen-1")
	if err != nil || string(again) != string(launchd) {
		t.Fatalf("unit render is not deterministic: %v", err)
	}
}

func TestL2ManagedRefFrozenInPlanDigestAndValidation(t *testing.T) {
	ref := "managed:" + strings.Repeat("b", 64)
	descriptor := r1Descriptor("conn_l2", "build", strings.Repeat("0", 64), "/tmp/l2-executable")
	descriptor.ManagedCredentialRef = ref
	if err := validateGatewayLaunchDescriptor(descriptor); err != nil {
		t.Fatalf("canonical managed ref rejected: %v", err)
	}
	bad := descriptor
	bad.ManagedCredentialRef = "managed:not-canonical"
	if err := validateGatewayLaunchDescriptor(bad); err == nil {
		t.Fatal("malformed managed ref accepted by descriptor validation")
	}
	anchor, err := newGatewayRegistrationAnchor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.IntegritySHA256 == "" {
		t.Fatal("anchor integrity missing")
	}
	// Changing the managed ref must change the anchor digest (plan digest freezes it).
	other := descriptor
	other.ManagedCredentialRef = "managed:" + strings.Repeat("c", 64)
	otherAnchor, err := newGatewayRegistrationAnchor(other)
	if err != nil {
		t.Fatal(err)
	}
	if otherAnchor.IntegritySHA256 == anchor.IntegritySHA256 {
		t.Fatal("managed ref change did not alter the frozen anchor digest")
	}
	if !credentials.IsManagedRef(ref) {
		t.Fatal("IsManagedRef rejected a canonical managed ref")
	}
}
