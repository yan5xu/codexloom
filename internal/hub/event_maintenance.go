package hub

import (
	"log"
	"time"
)

const eventMaintenanceInterval = time.Minute

func (h *Hub) eventMaintenanceLoop() {
	run := func() {
		report, err := h.st.MaintainEventLogs()
		if err != nil {
			log.Printf("[codex-loom] maintain event logs: %v", err)
			return
		}
		if report.Rotated+report.Compressed+report.Removed > 0 {
			log.Printf(
				"[codex-loom] event log maintenance: rotated=%d compressed=%d removed=%d removedBytes=%d",
				report.Rotated, report.Compressed, report.Removed, report.RemovedBytes,
			)
		}
	}
	run()
	ticker := time.NewTicker(eventMaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			run()
		}
	}
}
