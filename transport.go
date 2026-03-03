package dmxcast

// Transport sends merged DMX for an output universe.
type Transport interface {
	Send(dmx [512]byte) error
	Close() error
}
