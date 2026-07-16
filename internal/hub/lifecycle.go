package hub

// startWorker registers finite asynchronous work with the Hub lifecycle. The
// stopping flag and WaitGroup Add are serialized by h.mu, so Shutdown cannot
// begin waiting while a new worker is being admitted.
func (h *Hub) startWorker(work func()) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startWorkerLocked(work)
}

func (h *Hub) startWorkerLocked(work func()) bool {
	if h.stopping {
		return false
	}
	h.workers.Add(1)
	go func() {
		defer h.workers.Done()
		work()
	}()
	return true
}
