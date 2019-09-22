package x32

import (
	"fmt"
	"strconv"
	"strings"
	"net"

	"github.com/scgolang/osc"
	"github.com/golang/glog"
)

type ProxyConfig struct {
	ListenAddress string
	ReaperAddress string
	X32Address    string
}

type Proxy struct {
	server       *osc.UDPConn
	reaperClient *osc.UDPConn
	x32Client    *osc.UDPConn
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
	lConn, err := osc.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(lAddr), Port: lPort})
	if err != nil {
		return nil, fmt.Errorf("couldn't create listen UDP connection: %q", err)
	}
	xAddr, xPort, err := splitAddress(config.X32Address)
	if err != nil {
		return nil, fmt.Errorf("invalid X32Address: %q", err)
	}
	xConn, err := osc.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(xAddr), Port: xPort})
	if err != nil {
		return nil, fmt.Errorf("couldn't create x32 UDP connection: %q", err)
	}
	rAddr, rPort, err := splitAddress(config.ReaperAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid ReaperAddress: %q", err)
	}
	rConn, err := osc.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(rAddr), Port: rPort})
	if err != nil {
		return nil, fmt.Errorf("couldn't create reaper UDP connection: %q", err)
	}

	lConn.SetExactMatch(false)

	p := &Proxy{
		server:       lConn,
		reaperClient: rConn,
		x32Client:    xConn,
	}


	return p, nil
}

func (p *Proxy) ListenAndServe() error {
	p.server.SetExactMatch(false)
	return p.server.Serve(1, osc.Dispatcher{
		"/track/*/volume": osc.Method(func(msg osc.Message) error {
			glog.Info("got a message")
			return nil
	}),
})
}
