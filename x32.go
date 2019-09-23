package x32

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"github.com/scgolang/osc"
)

type ProxyConfig struct {
	ListenAddress string `yaml:"listenAddress"`
	ReaperAddress string `yaml:"reaperAddress"`
	X32Address    string `yaml:"x32Address"`

	Mapping map[string]string `yaml:"mapping"`
}

type Proxy struct {
	server       *osc.UDPConn
	reaperClient *osc.UDPConn
	x32Client    *osc.UDPConn

	mapping mapping
}

type mapping struct {
	reaper map[string]string
	x32    map[string]string
}

func splitAddress(a string) (string, int, error) {
	bits := strings.Split(a, ":")
	if c := len(bits); c != 2 {
		return "", 0, fmt.Errorf("invalid address:port - found %d parts, expected 2", c)
	}
	port, err := strconv.Atoi(bits[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port: %v", err)
	}
	return bits[0], port, nil
}

func parseRange(r string) (int, int, error) {
	rng := strings.Split(r, "-")
	switch l := len(rng); l {
	case 1:
		v, err := strconv.Atoi(rng[0])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range %v", err)
		}
		return v, v, nil
	case 2:
		lo, err := strconv.Atoi(rng[0])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range lower %v", err)
		}
		hi, err := strconv.Atoi(rng[1])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range upper %v", err)
		}
		if lo > hi {
			return -1, -1, fmt.Errorf("invalid range %d > %d", hi, lo)
		}

		return lo, hi, nil
	default:
		return -1, -1, fmt.Errorf("invalid range %q", r)
	}
}

func isReaperType(m string) error {
	if m != "track" {
		return fmt.Errorf("invalid reaper mapping type %s", m)
	}
	return nil
}

var validX32MappingTypes = map[string]bool{
	"ch":    true,
	"bus":   true,
	"dca":   true,
	"mtx":   true,
	"main":  true,
	"auxin": true,
	"fxrtn": true,
}

func isX32Type(m string) error {
	if !validX32MappingTypes[m] {
		return fmt.Errorf("invalid x32 mapping type %s", m)
	}
	return nil
}

func buildMapping(conf map[string]string) (*mapping, error) {
	ret := mapping{
		reaper: make(map[string]string),
		x32:    make(map[string]string),
	}
	for k, v := range conf {
		reaTrack := strings.Split(k, "/")
		if err := isReaperType(reaTrack[0]); err != nil {
			return nil, err
		}
		reaLo, reaHi, err := parseRange(reaTrack[1])
		if err != nil {
			return nil, err
		}
		x32Track := strings.Split(v, "/")
		if err := isX32Type(x32Track[0]); err != nil {
			return nil, err
		}
		x32Lo, x32Hi, err := parseRange(x32Track[1])
		if err != nil {
			return nil, err
		}
		if reaSpan, x32Span := reaHi-reaLo+1, x32Hi-x32Lo+1; reaSpan != x32Span {
			return nil, fmt.Errorf("invalid mapping - reaper range %d != x32 range %d", reaSpan, x32Span)
		}

		for reaN, x32N := reaLo, x32Lo; reaN <= reaHi; reaN, x32N = reaN+1, x32N+1 {
			reaK := fmt.Sprintf("%s/%d", reaTrack[0], reaN)
			x32K := fmt.Sprintf("%s/%d", x32Track[0], x32N)
			if _, ok := ret.reaper[reaK]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", reaK)
			}
			if _, ok := ret.x32[x32K]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", x32K)
			}
			ret.reaper[reaK] = x32K
			ret.x32[x32K] = reaK
		}
	}
	return &ret, nil
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

	mapping, err := buildMapping(config.Mapping)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		server:       lConn,
		reaperClient: rConn,
		x32Client:    xConn,
		mapping:      *mapping,
	}

	glog.Infof("Mapping:\n%v", *mapping)

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
