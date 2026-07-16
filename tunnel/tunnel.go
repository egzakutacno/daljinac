package tunnel

type Tunnel interface {
	Start()
	Stop()
	URL() string
	IsRunning() bool
}
