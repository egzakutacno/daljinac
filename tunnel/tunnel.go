package tunnel

import "time"

type Tunnel interface {
	Start()
	Stop()
	URL() string
	IsRunning() bool
	LastConnected() time.Time
}
