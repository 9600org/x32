package x32

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/hypebeast/go-osc/osc"
	"golang.org/x/sync/errgroup"
)

type ProxyConfig struct {
	ListenAddress string `yaml:"listenAddress"`
	ReaperAddress string `yaml:"reaperAddress"`
	X32Address    string `yaml:"x32Address"`

	Mapping map[string]string `yaml:"mapping"`
}

type Proxy struct {
	reaperServer *osc.Server
	reaperClient Client
	x32Server    *osc.Server
	x32Client    Client
	x32ServeConn net.PacketConn

	mapping mapping

	dispatcher osc.Dispatcher
}

type mapping struct {
	reaper map[string]x32Target
	x32    map[string]reaperTarget
}

type x32Target struct {
	strip     string
	statIndex int
}

type reaperTarget struct {
	track string
}

func splitAddress(a string) (string, int, error) {
	host, portString, err := net.SplitHostPort(a)
	if err != nil {
		return "", 0, err
	}
	if strings.Contains(host, ":") {
		host = fmt.Sprintf("[%s]", host)
	}
	port, err := net.LookupPort("udp", portString)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
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
	if m != "track" && m != "master" {
		return fmt.Errorf("invalid reaper mapping type %s", m)
	}
	return nil
}

// Valid x32 strip types and their -stat starting offset
var validX32MappingTypes = map[string]int{
	"ch":      0,
	"bus":     48,
	"dca":     72,
	"mtx":     64,
	"main/m":  70,
	"main/st": 71,
	"auxin":   32,
	"usb":     38,
	"fxrtn":   40,
}

func buildMapping(conf map[string]string) (*mapping, error) {
	ret := mapping{
		reaper: make(map[string]x32Target),
		x32:    make(map[string]reaperTarget),
	}
	for k, v := range conf {
		var x32Track, reaTrack []string
		var x32Lo, x32Hi, reaLo, reaHi int
		var err error

		switch v {
		case "main/st", "main/m":
			x32Track = []string{v}
		default:
			x32Track = strings.Split(v, "/")
			x32Lo, x32Hi, err = parseRange(x32Track[1])
			if err != nil && x32Track[0] != "main" {
				return nil, err
			}
		}
		x32StatIndexBase, ok := validX32MappingTypes[x32Track[0]]
		if !ok {
			return nil, fmt.Errorf("invalid x32 mapping type %s", x32Track[0])
		}

		switch k {
		case "master":
			if x32Lo != x32Hi {
				return nil, fmt.Errorf("mapping for /master should have 1 target, got %d", (x32Hi - x32Lo + 1))
			}
			x32K := x32Track[0]
			if x32Lo > 0 {
				x32K = fmt.Sprintf("%s/%02d", x32Track[0], x32Lo)
			}
			ret.reaper[k] = x32Target{strip: x32K, statIndex: x32StatIndexBase + x32Lo}
			ret.x32[x32K] = reaperTarget{track: k}
			continue
		default:
			reaTrack = strings.Split(k, "/")
			reaLo, reaHi, err = parseRange(reaTrack[1])
			if err != nil {
				return nil, err
			}
		}
		if err := isReaperType(reaTrack[0]); err != nil {
			return nil, err
		}

		// Handle all other mappings:
		if reaSpan, x32Span := reaHi-reaLo+1, x32Hi-x32Lo+1; reaSpan != x32Span {
			return nil, fmt.Errorf("invalid mapping - reaper range %d != x32 range %d", reaSpan, x32Span)
		}

		for reaN, x32N := reaLo, x32Lo; reaN <= reaHi; reaN, x32N = reaN+1, x32N+1 {
			reaK := fmt.Sprintf("%s/%d", reaTrack[0], reaN)
			x32K := fmt.Sprintf("%s/%02d", x32Track[0], x32N)
			if _, ok := ret.reaper[reaK]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", reaK)
			}
			if _, ok := ret.x32[x32K]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", x32K)
			}
			ret.reaper[reaK] = x32Target{strip: x32K, statIndex: x32StatIndexBase + x32N}
			ret.x32[x32K] = reaperTarget{track: reaK}
		}
	}
	return &ret, nil
}

type Client struct {
	Conn *net.UDPConn
}

func (c *Client) Send(p osc.Packet) error {
	data, err := p.MarshalBinary()
	if err != nil {
		return err
	}
	if _, err := c.Conn.Write(data); err != nil {
		return err
	}
	return nil
}

func NewProxy(config ProxyConfig) (*Proxy, error) {
	// Validate config addresses
	reaperServer := &osc.Server{Addr: config.ListenAddress}
	xServer := &osc.Server{}

	xAddr, xPort, err := splitAddress(config.X32Address)
	if err != nil {
		return nil, fmt.Errorf("invalid X32Address: %q", err)
	}
	xIP, err := net.LookupIP(xAddr)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup x32 IP: %s", err)
	}
	xServerConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: xIP[0], Port: xPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial x32: %s", err)
	}
	xClient := Client{xServerConn}

	rAddr, rPort, err := splitAddress(config.ReaperAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid ReaperAddress: %q", err)
	}
	rIP, err := net.LookupIP(rAddr)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup reaper IP: %s", err)
	}
	rClientConn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: rIP[0], Port: rPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial reaper: %v", err)
	}
	rClient := Client{rClientConn}

	mapping, err := buildMapping(config.Mapping)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		reaperServer: reaperServer,
		reaperClient: rClient,
		x32Server:    xServer,
		x32Client:    xClient,
		x32ServeConn: xServerConn,
		mapping:      *mapping,
	}

	return p, nil
}

func (p *Proxy) ListenAndServe() error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-t.C:
				// send xremote
				glog.Infof("xremote")
				if err := p.x32Client.Send(osc.NewMessage("/xremote")); err != nil {
					glog.Warningf("Failed to send xremote: %v", err)
				}
			case <-done:
				return
			}
		}
	}()

	p.reaperServer.Dispatcher = osc.NewOscDispatcher()
	p.buildReaperDispatcher(p.reaperServer.Dispatcher)

	p.x32Server.Dispatcher = osc.NewOscDispatcher()
	p.buildX32Dispatcher(p.x32Server.Dispatcher)

	errGroup := errgroup.Group{}
	errGroup.Go(func() error {
		return p.x32Server.Serve(p.x32ServeConn)
	})

	errGroup.Go(p.reaperServer.ListenAndServe)

	return errGroup.Wait()
}

type TargetType int

const (
	reaperTrack           TargetType = iota
	reaperTrackFromArg               = iota
	x32Strip                         = iota
	x32Stat                          = iota
	x32StatWithAddressArg            = iota
)

type targetTransform struct {
	targetType TargetType
	target     string
	transform  func([]*osc.Message) []*osc.Message
}

func FloatToInt(m []*osc.Message) []*osc.Message {
	for mi := range m {
		for i, a := range m[mi].Arguments {
			f, ok := a.(float32)
			if !ok {
				glog.Errorf("Attempting to read float32 from %T failed", a)
				continue
			}
			m[mi].Arguments[i] = int32(f)
		}
	}
	return m
}

func IntToFloat(m []*osc.Message) []*osc.Message {
	for mi := range m {
		for i, a := range m[mi].Arguments {
			f, ok := a.(int32)
			if !ok {
				glog.Errorf("Attempting to read int32 from %T failed", a)
				continue
			}
			m[mi].Arguments[i] = float32(f)
		}
	}
	return m
}

func NotInt(m []*osc.Message) []*osc.Message {
	for mi := range m {
		for i, a := range m[mi].Arguments {
			v, ok := a.(int32)
			if !ok {
				glog.Errorf("Attempting to read int32 from %T failed", a)
				continue
			}
			if v == 0 {
				v = 1
			} else {
				v = 0
			}
			m[mi].Arguments[i] = int32(v)
		}
	}
	return m
}

func DropArgs(m []*osc.Message) []*osc.Message {
	for i := range m {
		m[i].Arguments = nil
	}
	return m
}

func FilterUnselect(m []*osc.Message) []*osc.Message {
	ret := make([]*osc.Message, 0, len(m))
	for mi := range m {
		if len(m[mi].Arguments) == 1 {
			f, ok := m[mi].Arguments[0].(float32)
			if !ok {
				glog.Errorf("Attempting to read float from %T failed", m[mi].Arguments[0])
			} else {
				if f == 0 {
					continue
				}
			}
		}
		ret = append(ret, m[mi])
	}
	return ret
}

func (t *targetTransform) Apply(m *osc.Message) []*osc.Message {
	if t.transform != nil {
		return t.transform([]*osc.Message{m})
	}
	return []*osc.Message{m}
}

var (
	// reaperX32StripMap is a map of all /track/${ID}/... subaddresses which
	// are sent by Reaper, and their corresponding X32 targets.
	reaperX32StripMap = map[string]targetTransform{
		"volume": targetTransform{target: "mix/fader", targetType: x32Strip},
		"mute": targetTransform{target: "mix/on", targetType: x32Strip,
			transform: func(m []*osc.Message) []*osc.Message {
				return NotInt(FloatToInt(m))
			},
		},
		"pan": targetTransform{target: "mix/pan", targetType: x32Strip},
		"select": targetTransform{target: "-stat/selidx", targetType: x32StatWithAddressArg,
			transform: func(m []*osc.Message) []*osc.Message {
				return DropArgs(FilterUnselect(m))
			},
		},
		"solo": targetTransform{target: "-stat/solosw", targetType: x32Stat,
			transform: FloatToInt,
		},
	}

	// x32eaperStripMap is a map of all addresses which
	// are sent by Reaper, and their corresponding X32 targets.
	x32ReaperStripMap = map[string]targetTransform{
		"mix/fader": targetTransform{target: "volume", targetType: reaperTrack},
		"mix/on": targetTransform{target: "mute", targetType: reaperTrack,
			transform: func(m []*osc.Message) []*osc.Message {
				return IntToFloat(NotInt(m))
			},
		},
		"mix/pan": targetTransform{target: "pan", targetType: reaperTrack},
	}

	x32ReaperStatMap = map[string]targetTransform{
		"-stat/solosw": targetTransform{target: "solo", targetType: reaperTrack,
			transform: IntToFloat,
		},
	}

	x32ReaperFanoutStatMap = map[string]targetTransform{
		"-stat/selidx": targetTransform{target: "select", targetType: reaperTrackFromArg,
			transform: DropArgs,
		},
	}
)

//x32 trackids
// ch 1-32
// auxin 33-48
// bus 49-64
// matrix (1-6) 65-70
// Main mid 71
// Main LR 72
// DCA 73-82
//
//
//  /track/../mute <-> /{type}/mix/on (NOT)
//  /track/../pan <-> /{type}/mix/pan
//  /track/../volume <-> /{type}/mix/fader
//  /track/../select <-> /-stat/selidx
//  /track/../solo <-> /-stat/solosw

func (p *Proxy) buildReaperDispatcher(d *osc.OscDispatcher) error {
	for rPfx, xTarget := range p.mapping.reaper {
		rPfx := rPfx
		xTarget := xTarget
		for rSfx, tt := range reaperX32StripMap {
			rSfx := rSfx
			tt := tt
			switch tt.targetType {
			case x32Strip:
				reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
				x32Addr := fmt.Sprintf("/%s/%s", xTarget.strip, tt.target)
				d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
					glog.V(1).Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							glog.Errorf("Failed to send message to X32: %s", err)
						}
					}
				})
			case x32Stat:
				reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
				x32Addr := fmt.Sprintf("/%s/%02d", tt.target, xTarget.statIndex)
				d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
					glog.V(1).Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							glog.Errorf("Failed to send message to X32: %s", err)
						}
					}
				})
			case x32StatWithAddressArg:
				reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
				x32Addr := fmt.Sprintf("/%s", tt.target)
				d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
					glog.V(1).Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Arguments = append([]interface{}{int32(xTarget.statIndex - 1)}, x32Msg.Arguments...)
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							glog.Errorf("Failed to send message to X32: %s", err)
						}
					}
				})

			default:
				glog.Fatalf("Found unexpected targetType %v", tt.targetType)
			}
		}
	}
	return nil
}

func (p *Proxy) buildX32Dispatcher(d *osc.OscDispatcher) error {
	for xPfx, rTarget := range p.mapping.x32 {
		xPfx := xPfx
		rTarget := rTarget

		// Track addresses
		for xSfx, tt := range x32ReaperStripMap {
			xSfx := xSfx
			tt := tt
			switch tt.targetType {
			case reaperTrack:
				x32Addr := fmt.Sprintf("/%s/%s", xPfx, xSfx)
				reaAddr := fmt.Sprintf("/%s/%s", rTarget.track, tt.target)
				glog.Infof("Listen on %s", x32Addr)
				d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
					glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = reaAddr
						if err := p.reaperClient.Send(x32Msg); err != nil {
							glog.Errorf("Failed to send message to Reaper: %s", err)
						}
					}
				})
			}
		}

		// Stat addresses
		for xSfx, tt := range x32ReaperStatMap {
			xSfx := xSfx
			tt := tt
			switch tt.targetType {
			case reaperTrack:
				x32Addr := fmt.Sprintf("/%s/%s", xPfx, xSfx)
				reaAddr := fmt.Sprintf("/%s/%s", rTarget.track, tt.target)
				d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
					glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = reaAddr
						if err := p.reaperClient.Send(x32Msg); err != nil {
							glog.Errorf("Failed to send message to Reaper: %s", err)
						}
					}
				})
			default:
				glog.Fatalf("Found unexpected stat targetType %v", tt.targetType)
			}
		}
	}

	// Fan-out stat
	for xStat, tt := range x32ReaperFanoutStatMap {
		xStat := xStat
		tt := tt
		switch tt.targetType {
		case reaperTrackFromArg:
			x32Addr := fmt.Sprintf("/%s", xStat)
			d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
				for _, x32Msg := range tt.Apply(msg) {
					if len(x32Msg.Arguments) == 0 {
						glog.Errorf("Got fanout stat message with zero args")
						continue
					}
					id, ok := x32Msg.Arguments[0].(int32)
					if !ok {
						glog.Errorf("Got non-int32 fanout arg[0] of type %T", x32Msg.Arguments[0])
						continue
					}
					x32Addr := fmt.Sprintf("/track/%02d/%s", id, tt.target)
					x32Msg.Address = x32Addr
					if err := p.reaperClient.Send(x32Msg); err != nil {
						glog.Errorf("Failed to send message to Reaper: %s", err)
					}
				}
			})
		default:
			glog.Fatalf("Found unexpected fanout targetType %v", tt.targetType)
		}
	}
	return nil
}
