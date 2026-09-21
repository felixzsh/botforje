package channel

import "time"

func (a *Adapter) loop() {
	defer close(a.stopped)
	for {
		select {
		case <-a.stop:
			return
		case <-a.reconnect:
			a.reconnectOnce()
		}
	}
}

func (a *Adapter) reconnectOnce() {
	delay := a.backoff.Next()
	a.log.Debug("reconnecting", "delay", delay)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-a.stop:
		return
	case <-timer.C:
	}
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return
	}
	if err := client.Connect(); err != nil {
		a.log.Warn("reconnect failed", "error", err, "attempt", a.backoff.attempt)
		a.signalReconnect()
	}
}

func (a *Adapter) signalReconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

func (a *Adapter) stopReconnecting() {
	a.stopOnce.Do(func() { close(a.stop) })
}
