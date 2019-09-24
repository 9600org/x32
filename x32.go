package x32

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

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
	"mainMid": 70,
	"mainLR":  71,
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
		reaTrack := strings.Split(k, "/")
		if err := isReaperType(reaTrack[0]); err != nil {
			return nil, err
		}

		// TODO: fix master

		reaLo, reaHi, err := parseRange(reaTrack[1])
		if err != nil {
			return nil, err
		}
		x32Track := strings.Split(v, "/")
		x32StatIndexBase, ok := validX32MappingTypes[x32Track[0]]
		if !ok {
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

	return p, nil
}

func (p *Proxy) ListenAndServe() error {
	p.server.SetExactMatch(false)

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-t.C:
				// send xremote
				glog.Infof("xremote")
				if err := p.x32Client.Send(osc.Message{Address: "/xremote"}); err != nil {
					glog.Warningf("Failed to send xremote: %v", err)
				}
			case <-done:
				return
			}
		}
	}()

	d := p.buildReaperDispatcher()

	return p.server.Serve(1, d)
}

type TargetType int

const (
	reaperTrack           TargetType = iota
	x32Strip                         = iota
	x32Stat                          = iota
	x32StatWithAddressArg            = iota
)

type targetTransform struct {
	targetType TargetType
	target     string
	transform  func([]osc.Message) []osc.Message
}

func FloatToInt(m []osc.Message) []osc.Message {
	for mi := range m {
		for i, a := range m[mi].Arguments {
			f, err := a.ReadFloat32()
			if err != nil {
				glog.Errorf("Attempting to read float from %v failed: %v", a.Typetag(), err)
				continue
			}
			m[mi].Arguments[i] = osc.Int(f)
		}
	}
	return m
}

func NotInt(m []osc.Message) []osc.Message {
	for mi := range m {
		for i, a := range m[mi].Arguments {
			v, err := a.ReadInt32()
			if err != nil {
				glog.Errorf("Attempting to read Int from %v failed: %v", a.Typetag(), err)
				continue
			}
			if v == 0 {
				v = 1
			} else {
				v = 0
			}
			m[mi].Arguments[i] = osc.Int(v)
		}
	}
	return m
}

func DropArgs(m []osc.Message) []osc.Message {
	for i := range m {
		m[i].Arguments = nil
	}
	return m
}

func FilterUnselect(m []osc.Message) []osc.Message {
	ret := make([]osc.Message, 0, len(m))
	for mi := range m {
		if len(m[mi].Arguments) == 1 {
			f, err := m[mi].Arguments[0].ReadFloat32()
			if err != nil {
				glog.Errorf("Attempting to read float from %v failed: %v", m[mi].Arguments[0].Typetag(), err)
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

func (t *targetTransform) Apply(m osc.Message) []osc.Message {
	if t.transform != nil {
		return t.transform([]osc.Message{m})
	}
	return []osc.Message{m}
}

var (
	reaperX32StripMap = map[string]targetTransform{
		"volume": targetTransform{target: "mix/fader", targetType: x32Strip},
		"mute": targetTransform{target: "mix/on", targetType: x32Strip,
			transform: func(m []osc.Message) []osc.Message {
				return NotInt(FloatToInt(m))
			},
		},
		"pan": targetTransform{target: "mix/pan", targetType: x32Strip},
		"select": targetTransform{target: "-stat/selidx", targetType: x32StatWithAddressArg,
			transform: func(m []osc.Message) []osc.Message {
				return DropArgs(FilterUnselect(m))
			},
		},
		"solo": targetTransform{target: "-stat/solosw", targetType: x32Stat,
			transform: FloatToInt,
		},
	}

	x32ReaperStripMap = map[string]targetTransform{
		"mix/fader": targetTransform{target: "volume"},
		"mix/on":    targetTransform{target: "mute", transform: NotInt},
		"mix/pan":   targetTransform{target: "pan"},
		"mix/solo":  targetTransform{target: "solo"}, //?
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

func (p *Proxy) buildReaperDispatcher() osc.Dispatcher {
	r := make(osc.Dispatcher)

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
				r[reaAddr] = osc.Method(func(msg osc.Message) error {
					glog.Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							return err
						}
					}
					return nil
				})
			case x32Stat:
				reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
				x32Addr := fmt.Sprintf("/%s/%02d", tt.target, xTarget.statIndex)
				r[reaAddr] = osc.Method(func(msg osc.Message) error {
					glog.Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							return err
						}
					}
					return nil
				})
			case x32StatWithAddressArg:
				reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
				x32Addr := fmt.Sprintf("/%s", tt.target)
				r[reaAddr] = osc.Method(func(msg osc.Message) error {
					glog.Infof("R-> %s %v", reaAddr, msg.Arguments)
					for _, x32Msg := range tt.Apply(msg) {
						x32Msg.Arguments = append([]osc.Argument{osc.Int(xTarget.statIndex)}, x32Msg.Arguments...)
						x32Msg.Address = x32Addr
						if err := p.x32Client.Send(x32Msg); err != nil {
							return err
						}
					}
					return nil
				})

			default:
				glog.Fatalf("Found unexpected targetType %v", tt.targetType)
			}
		}
	}
	r[""] = osc.Method(func(msg osc.Message) error {
		glog.Infof("R?-> %v", msg.Address)
		return nil
	})

	glog.Infof("R-> mapping:\n%#v", r)

	return r
}
