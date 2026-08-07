//go:build !unix

package httpapi

import (
	"errors"

	"github.com/yan5xu/codex-loom/internal/hub"
)

func (s *Server) prepareManagedGatewayRestart(hub.PlatformConnection) (managedGatewayRestartPlan, error) {
	return managedGatewayRestartPlan{}, nil
}

func writeManagedGatewayRestartUnit(string, []byte) error {
	return errors.New("managed gateway restart is unsupported on this platform")
}
