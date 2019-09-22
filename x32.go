package x32

import (
	"fmt"
	"github.com/hypebeast/go-osc/osc"
	"strconv"
	"strings"
)

type ProxyConfig struct {
	ListenAddress string
	ReaperAddress string
	X32Address    string
}

type Proxy struct {
	server       *osc.Server
	reaperClient *osc.Client
	x32Client    *osc.Client
}

func splitAddress(a string) (string, int, error) {
	bits := strings.Split(a, ":")
	if c := len(bits); c != 2 {
		return "", 0, fmt.Errorf("invalid address:port - found %d ':', expected 1", c)
	}
	port, err := strconv.Atoi(bits[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %q", err)
	}
	return bits[0], port, nil
}

func NewProxy(config ProxyConfig) (*Proxy, error) {
	// Validate config addresses
	lAddr, lPort, err := splitAddress(config.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid ListenAddress: %q", err)
	}
	xAddr, xPort, err := splitAddress(config.ListenAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid X32Address: %q", err)
	}
	rAddr, rPort, err := splitAddress(config.ReaperAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid ReaperAddress: %q", err)
	}

	p := &Proxy{
		server:       &osc.Server{Addr: fmt.Sprintf("%s:%d", lAddr, lPort)},
		reaperClient: osc.NewClient(rAddr, rPort),
		x32Client:    osc.NewClient(xAddr, xPort),
	}

	p.server.Handle("/message/address", func(msg *osc.Message) {
		osc.PrintMessage(msg)
	})

	return p, nil
}

func (p *Proxy) ListenAndServe() error {
	return p.server.ListenAndServe()
}
