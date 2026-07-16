package hub

func (h *Hub) Shutdown() {
	h.mu.Lock()
	h.stopping = true
	for _, rt := range h.runtimes {
		if rt.activeTurn != nil && !rt.activeTurn.finished {
			rt.activeTurn.finished = true
			if rt.activeTurn.stopWatchdog != nil {
				close(rt.activeTurn.stopWatchdog)
			}
		}
	}
	h.mu.Unlock()
	h.stopOnce.Do(func() {
		if h.stop != nil {
			close(h.stop)
		}
	})
	h.background.Wait()
	h.workers.Wait()
	h.mu.Lock()
	host := h.codexHost
	h.codexHost = nil
	h.remoteRuntime = nil
	h.persistRuntimeProjectionLocked()
	h.mu.Unlock()
	if host != nil {
		host.client.Close()
	}
}
