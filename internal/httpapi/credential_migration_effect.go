package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) reconcileMigrationGatewayEffect(_ context.Context, connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt, rollback bool) (hub.CredentialMigrationGatewayReceipt, bool, error) {
	expectedReceipt := receipt.GatewayReceipt
	if rollback {
		expectedReceipt = receipt.RollbackGatewayReceipt
	}
	if expectedReceipt == nil {
		return hub.CredentialMigrationGatewayReceipt{}, false, nil
	}
	expected := *expectedReceipt
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
	if rollback {
		expected.Status = "restored"
	} else {
		expected.Status = "verified"
	}
	expected.HeartbeatAt = current.LastHeartbeatAt
	return expected, true, nil
}

func migrationGatewayEffectID(receiptID, targetRef, kind string, attempt int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(receiptID) + "\x00" + strings.TrimSpace(kind) + "\x00" + strconv.Itoa(attempt) + "\x00" + strings.TrimSpace(targetRef)))
	return "geff_" + hex.EncodeToString(sum[:16])
}

func migrationGatewayGeneration(effectID, build string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(effectID) + "\x00" + strings.TrimSpace(build)))
	return "ggen_" + hex.EncodeToString(sum[:16])
}

func gatewayProcessEvidenceMatches(connection hub.PlatformConnection, expected hub.CredentialMigrationGatewayReceipt) bool {
	return expected.Generation != "" && expected.Build != "" && expected.ExecutableSHA256 != "" &&
		connection.GatewayGeneration == expected.Generation && connection.GatewayBuild == expected.Build &&
		connection.GatewayExecutableSHA256 == expected.ExecutableSHA256
}

func preserveMigrationGatewayEffectTarget(expected *hub.CredentialMigrationGatewayReceipt, observed hub.CredentialMigrationGatewayReceipt) hub.CredentialMigrationGatewayReceipt {
	if expected == nil {
		return observed
	}
	observed.Manager = expected.Manager
	observed.Service = expected.Service
	observed.Build = expected.Build
	observed.ExecutableSHA256 = expected.ExecutableSHA256
	observed.Generation = expected.Generation
	observed.AnchorID = expected.AnchorID
	return observed
}

func (s *Server) matchMigrationConnectionControl(connection hub.PlatformConnection) error {
	current, err := s.hub.SnapshotConnectionControl(connection.ID)
	if err != nil {
		return err
	}
	if current.Provider != connection.Provider || current.AccountRef != connection.AccountRef ||
		current.ScopeRef != connection.ScopeRef || current.CredentialRef != connection.CredentialRef ||
		current.Enabled != connection.Enabled || current.SupersededBy != connection.SupersededBy ||
		current.ArchivedAt != connection.ArchivedAt {
		return &hub.HubError{Status: 409, Message: "credential migration connection identity changed"}
	}
	return nil
}
