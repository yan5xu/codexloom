package httpapi

import (
	"errors"

	"github.com/yan5xu/codex-loom/internal/backup"
	"github.com/yan5xu/codex-loom/internal/buildinfo"
	"github.com/yan5xu/codex-loom/internal/hub"
)

var verifyManagedCredentialWriteFloor = func(s *Server) error {
	if s == nil || s.st == nil || !buildinfo.ValidBuildIdentity(s.build.Commit) {
		return errors.New("accepted rollback build identity is unavailable")
	}
	items, err := backup.List(s.st.Dir())
	if err != nil {
		return err
	}
	for _, item := range items {
		verification := backup.Verify(item.Path)
		if verification.ValidateRollbackFloor(backup.CurrentManifestVersion, s.build.Commit) == nil {
			return nil
		}
	}
	return errors.New("no verified credential-excluding rollback build is available")
}

func (s *Server) requireManagedCredentialWriteFloor() error {
	if err := verifyManagedCredentialWriteFloor(s); err != nil {
		return &hub.HubError{Status: 409, Message: "credential_rollback_build_floor_unavailable"}
	}
	return nil
}
