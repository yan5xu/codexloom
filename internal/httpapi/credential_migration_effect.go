package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) reconcileMigrationGatewayEffect(_ context.Context, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt, rollback bool) (hub.CredentialMigrationGatewayReceipt, bool, error) {
	if rollback {
		return hub.CredentialMigrationGatewayReceipt{}, false, nil
	}
	if receipt.GatewayReceipt == nil {
		return hub.CredentialMigrationGatewayReceipt{}, false, nil
	}
	expected := *receipt.GatewayReceipt
	if expected.Status == "not_applicable" && managedGatewayProvider(connection.Provider) == "" {
		expected.Status = "verified"
		return expected, true, nil
	}
	current, err := s.migrationConnection(connection.ID)
	if err != nil {
		return hub.CredentialMigrationGatewayReceipt{}, false, err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, current.LastHeartbeatAt)
	preparedAt, prepareErr := time.Parse(time.RFC3339Nano, receipt.UpdatedAt)
	if err != nil || prepareErr != nil || !observedAt.After(preparedAt) || current.Status != "connected" || !gatewayProcessEvidenceMatches(current, expected) {
		return hub.CredentialMigrationGatewayReceipt{}, false, nil
	}
	expected.Status = "verified"
	expected.HeartbeatAt = current.LastHeartbeatAt
	return expected, true, nil
}

func migrationGatewayEffectID(receiptID, targetRef string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(receiptID) + "\x00" + strings.TrimSpace(targetRef)))
	return "geff_" + hex.EncodeToString(sum[:16])
}

func migrationGatewayGeneration(effectID, build string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(effectID) + "\x00" + strings.TrimSpace(build)))
	return "ggen_" + hex.EncodeToString(sum[:16])
}

func gatewayProcessEvidenceMatches(connection hub.PlatformConnection, expected hub.CredentialMigrationGatewayReceipt) bool {
	return expected.Build != "" && expected.ExecutableSHA256 != "" &&
		connection.GatewayGeneration == expected.Generation && connection.GatewayBuild == expected.Build &&
		connection.GatewayExecutableSHA256 == expected.ExecutableSHA256
}

func (s *Server) matchMigrationConnectionControl(connection hub.PlatformConnection) error {
	return s.hub.MatchConnectionControl(hub.ConnectionControlSnapshot{
		ID: connection.ID, Provider: connection.Provider, AccountRef: connection.AccountRef,
		ScopeRef: connection.ScopeRef, CredentialRef: connection.CredentialRef, Enabled: connection.Enabled,
		SupersededBy: connection.SupersededBy, ArchivedAt: connection.ArchivedAt,
	})
}
