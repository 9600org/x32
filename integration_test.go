package x32

import (
	"testing"

	"github.com/9600org/go-osc/osc"
)

var testConfig = ProxyConfig{
	ListenAddress: "localhost:0",
	ReaperAddress: "localhost:0",
	X32Address:    "localhost:0",

	Mapping: map[string]string{
		"master":      "main/st",
		"track/3":     "ch/1",
		"track/14-16": "bus/4-6",
	},
}

type chanClient struct {
	fromProxy chan osc.Packet
	toProxy   chan osc.Packet
}

func (c *chanClient) Send(p osc.Packet) error {
	c.fromProxy <- p
	return nil
}

// newIntegrationEnv returns a new environment for end-to-end integration
// tests.
// Returns a proxy,
func newIntegrationEnv(t *testing.T) (*Proxy, Client, Client, func()) {
	t.Helper()

	fX32 := &chanClient{
		fromProxy: make(chan osc.Packet, 10),
		toProxy:   make(chan osc.Packet, 10),
	}
	fReaper := &chanClient{
		fromProxy: make(chan osc.Packet, 10),
		toProxy:   make(chan osc.Packet, 10),
	}
	p, err := NewProxy(testConfig)
	if err != nil {
		t.Fatalf("Failed to create new proxy: %v", err)
	}

	p.x32Client = fX32
	p.reaperClient = fReaper
	go func() {
		for pack := range fX32.toProxy {
			p.dispatcher.Dispatch(pack)
		}
	}()
	go func() {
		for pack := range fReaper.toProxy {
			p.dispatcher.Dispatch(pack)
		}
	}()

	done := func() {
		close(fX32.fromProxy)
		close(fX32.toProxy)
		close(fReaper.fromProxy)
		close(fReaper.toProxy)
	}

	return p, fX32, fReaper, done
}

func TestNewProxy(t *testing.T) {
	_, err := NewProxy(testConfig)
	if err != nil {
		t.Fatalf("NewProxy = %v, expected no error", err)
	}
}

func TestStrips(t *testing.T) {
	_, _, _, done := newIntegrationEnv(t)
	defer done()

	for _, test := range []struct {
		name string
	}{} {
		t.Run(test.name, func(t *testing.T) {
		})
	}
}
