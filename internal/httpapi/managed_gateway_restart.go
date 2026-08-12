package httpapi

import "log"

// RestartManagedGateways is the startup bridge into the private Runtime R1
// coordinator. Legacy/provider Connections without an adopted typed launch
// plan deliberately produce no service effect.
func (s *Server) RestartManagedGateways() {
	if err := s.hub.RestartGatewayProcesses(); err != nil {
		log.Printf("[codex-loom] reconcile/restart typed Gateway processes: %v", err)
	}
}
