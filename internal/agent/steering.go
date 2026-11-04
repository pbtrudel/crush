package agent

// steeringSignal releases tools that block waiting for external work when
// the user sends a new prompt. The channel is closed exactly once.
type steeringSignal struct {
	ch     chan struct{}
	closed bool
}

// beginSteering registers a steering signal for the session and returns its
// channel. A signal left over from an earlier run is closed so a stale
// waiter is released.
func (a *sessionAgent) beginSteering(sessionID string) chan struct{} {
	a.steeringMu.Lock()
	defer a.steeringMu.Unlock()
	if a.steering == nil {
		a.steering = make(map[string]*steeringSignal)
	}
	if old, ok := a.steering[sessionID]; ok && !old.closed {
		close(old.ch)
		old.closed = true
	}
	signal := &steeringSignal{ch: make(chan struct{})}
	a.steering[sessionID] = signal
	return signal.ch
}

// signalSteering releases any tool waiting on the session's steering signal.
func (a *sessionAgent) signalSteering(sessionID string) {
	a.steeringMu.Lock()
	defer a.steeringMu.Unlock()
	signal, ok := a.steering[sessionID]
	if !ok || signal.closed {
		return
	}
	close(signal.ch)
	signal.closed = true
}

// endSteering removes the run's steering signal. A signal registered by a
// newer run is left in place.
func (a *sessionAgent) endSteering(sessionID string, ch chan struct{}) {
	a.steeringMu.Lock()
	defer a.steeringMu.Unlock()
	signal, ok := a.steering[sessionID]
	if !ok || signal.ch != ch {
		return
	}
	delete(a.steering, sessionID)
}
