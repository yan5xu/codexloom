package httpapi

import (
	"sort"
	"strings"

	"github.com/yan5xu/codex-loom/internal/hub"
)

// lockCredentialIdentityMutation serializes provider-specific setup/repair
// work with migration and rollback for every existing Connection it may
// mutate. Reservation is checked only after all locks are held, so no secret,
// provider, or service hook can run for a Connection with a durable active
// migration.
func (s *Server) lockCredentialIdentityMutation(connectionIDs ...string) (func(), error) {
	seen := map[string]bool{}
	ids := make([]string, 0, len(connectionIDs))
	for _, connectionID := range connectionIDs {
		connectionID = strings.TrimSpace(connectionID)
		if connectionID == "" || seen[connectionID] {
			continue
		}
		seen[connectionID] = true
		ids = append(ids, connectionID)
	}
	sort.Strings(ids)
	unlocks := make([]func(), 0, len(ids))
	unlockAll := func() {
		for index := len(unlocks) - 1; index >= 0; index-- {
			unlocks[index]()
		}
	}
	for _, connectionID := range ids {
		unlocks = append(unlocks, s.lockCredentialMigration("connection:"+connectionID))
	}
	if err := s.hub.RequireCredentialMigrationsIdle(ids...); err != nil {
		unlockAll()
		return func() {}, err
	}
	return unlockAll, nil
}

func providerConnectionIDs(connections []hub.PlatformConnection, provider string, match func(hub.PlatformConnection) bool) []string {
	provider = credentialProviderFamily(provider)
	result := []string{}
	for _, connection := range connections {
		if credentialProviderFamily(connection.Provider) == provider && connection.ArchivedAt == "" && match(connection) {
			result = append(result, connection.ID)
		}
	}
	return result
}

func credentialProviderFamily(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "feishu" {
		return "lark"
	}
	return provider
}
