//go:build !unix

package httpapi

import (
	"context"
	"errors"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) preflightMigrationGateway(connection hub.PlatformConnection) error {
	if connection.Provider == "github" {
		return nil
	}
	return errors.New("managed gateway migration is unsupported on this platform")
}

func (s *Server) activateMigrationGateway(context.Context, hub.PlatformConnection, string, string, string) (hub.CredentialMigrationGatewayReceipt, error) {
	return hub.CredentialMigrationGatewayReceipt{Status: "unsupported"}, errors.New("managed gateway migration is unsupported on this platform")
}

func (s *Server) rollbackMigrationGateway(context.Context, hub.PlatformConnection, hub.CredentialMigrationReceipt) (hub.CredentialMigrationGatewayReceipt, error) {
	return hub.CredentialMigrationGatewayReceipt{Status: "unsupported"}, errors.New("managed gateway rollback is unsupported on this platform")
}
