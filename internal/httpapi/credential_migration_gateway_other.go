//go:build !unix

package httpapi

import (
	"context"
	"errors"

	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) preflightMigrationGateway(connection hub.PlatformConnection) error {
	if connection.Provider == "github" {
		return nil
	}
	return errors.New("managed gateway migration is unsupported on this platform")
}

func (s *Server) prepareMigrationGatewayEffect(connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef); err != nil {
		return "", hub.CredentialMigrationGatewayReceipt{}, err
	}
	if connection.Provider != "github" {
		return "", hub.CredentialMigrationGatewayReceipt{}, errors.New("managed gateway migration is unsupported on this platform")
	}
	if !buildinfo.ValidBuildIdentity(s.build.Commit) {
		return "", hub.CredentialMigrationGatewayReceipt{}, errors.New("gateway build identity is unavailable")
	}
	effectID := migrationGatewayEffectID(receipt.ID, receipt.TargetCredentialRef, "activation", receipt.GatewayEffectAttempt+1)
	return effectID, hub.CredentialMigrationGatewayReceipt{
		Status: "not_applicable", Build: s.build.Commit,
		Generation: migrationGatewayGeneration(effectID, s.build.Commit),
	}, nil
}

func (s *Server) prepareMigrationGatewayRollbackEffect(connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) (string, hub.CredentialMigrationGatewayReceipt, error) {
	if err := s.hub.MatchCredentialMigrationIdentity(receipt, receipt.PreviousCredentialRef, receipt.TargetCredentialRef); err != nil {
		return "", hub.CredentialMigrationGatewayReceipt{}, err
	}
	if connection.Provider != "github" {
		return "", hub.CredentialMigrationGatewayReceipt{}, errors.New("managed gateway rollback is unsupported on this platform")
	}
	attempt := receipt.RollbackEffectAttempt + 1
	effectID := migrationGatewayEffectID(receipt.ID, receipt.PreviousCredentialRef, "rollback", attempt)
	return effectID, hub.CredentialMigrationGatewayReceipt{Status: "not_applicable"}, nil
}

func (s *Server) preflightMigrationGatewayRollback(connection hub.PlatformConnection, receipt hub.CredentialMigrationReceipt) error {
	_, _, err := s.prepareMigrationGatewayRollbackEffect(connection, receipt)
	return err
}

func (s *Server) activateMigrationGateway(_ context.Context, connection hub.PlatformConnection, _, _, _ string, prepared hub.CredentialMigrationGatewayReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	if connection.Provider == "github" {
		prepared.Status = "not_applicable"
		return prepared, nil
	}
	return hub.CredentialMigrationGatewayReceipt{Status: "unsupported"}, errors.New("managed gateway migration is unsupported on this platform")
}

func (s *Server) rollbackMigrationGateway(context.Context, hub.PlatformConnection, hub.CredentialMigrationReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	return hub.CredentialMigrationGatewayReceipt{Status: "unsupported"}, errors.New("managed gateway rollback is unsupported on this platform")
}
