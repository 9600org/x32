package x32

import (
	"fmt"
	"net"
	"reflect"
	"regexp"
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

	NameHints map[string]string `yaml:"nameHints"`
}

type Proxy struct {
	reaperServer *osc.Server
	reaperClient Client
	x32Server    *osc.Server
	x32Client    Client
	x32ServeConn net.PacketConn

	trackMap trackMap

	nameHints []nameHint

	dispatcher osc.Dispatcher
}

type trackMap struct {
	reaper       map[string]mapping
	x32          map[string]mapping
	x32StatIndex map[int32]mapping
}

type mapping struct {
	reaperPrefix     string
	reaperTrackIndex int32
	x32Prefix        string
	x32StatIndex     int32
}

var colours = map[string]int{
	"OFF":  0,
	"RD":   1,
	"GN":   2,
	"YE":   3,
	"BL":   4,
	"MG":   5,
	"CY":   6,
	"WH":   7,
	"OFFi": 8,
	"RDi":  9,
	"GNi":  10,
	"YEi":  11,
	"BLi":  12,
	"MGi":  13,
	"CYi":  14,
	"WHi":  15,
}

type nameHint struct {
	matcher *regexp.Regexp
	icon    int
	colour  int
}

func newNameHint(m, hint string) (*nameHint, error) {
	ret := &nameHint{}
	var err error
	ret.matcher, err = regexp.Compile(m)
	if err != nil {
		return nil, err
	}
	hintBits := strings.Split(hint, " ")
	if len(hintBits) >= 1 {
		ret.icon, err = strconv.Atoi(hintBits[0])
		if err != nil {
			return nil, err
		}
	}
	if len(hintBits) == 2 {
		ret.colour = colours[hintBits[1]]
	}
	return ret, nil
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

func parseRange(r string) (int32, int32, error) {
	rng := strings.Split(r, "-")
	switch l := len(rng); l {
	case 1:
		v, err := strconv.Atoi(rng[0])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range %v", err)
		}
		return int32(v), int32(v), nil
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

		return int32(lo), int32(hi), nil
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
var validX32MappingTypes = map[string]int32{
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

func buildTrackMap(conf map[string]string) (*trackMap, error) {
	ret := trackMap{
		reaper:       make(map[string]mapping),
		x32:          make(map[string]mapping),
		x32StatIndex: make(map[int32]mapping),
	}
	for k, v := range conf {
		var x32Track, reaTrack []string
		var x32Lo, x32Hi, reaLo, reaHi int32
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
			x32StatIndex := x32StatIndexBase
			mapping := mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: k, reaperTrackIndex: -1}
			ret.reaper[k] = mapping
			ret.x32[x32K] = mapping
			ret.x32StatIndex[x32StatIndex] = mapping
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
			x32StatIndex := x32StatIndexBase + x32N - 1
			mapping := mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: reaK, reaperTrackIndex: reaN}
			ret.reaper[reaK] = mapping
			ret.x32[x32K] = mapping
			ret.x32StatIndex[x32StatIndex] = mapping
		}
	}
	return &ret, nil
}

func buildNameHints(src map[string]string) ([]nameHint, error) {
	r := make([]nameHint, 0, len(src))
	for k, v := range src {
		nh, err := newNameHint(k, v)
		if err != nil {
			return nil, err
		}
		r = append(r, *nh)
	}
	return r, nil
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

	trackMap, err := buildTrackMap(config.Mapping)
	if err != nil {
		return nil, err
	}

	nameHints, err := buildNameHints(config.NameHints)
	if err != nil {
		return nil, err
	}

	p := &Proxy{
		reaperServer: reaperServer,
		reaperClient: rClient,
		x32Server:    xServer,
		x32Client:    xClient,
		x32ServeConn: xServerConn,
		trackMap:     *trackMap,
		nameHints:    nameHints,
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
	transform  func(*targetTransform, mapping, osc.Message) ([]osc.Message, error)
	trackMap   *trackMap
}

func FloatToInt(a interface{}) (int32, error) {
	f, ok := a.(float32)
	if !ok {
		return 0, fmt.Errorf("failed to convert argument type %T to float32", a)
	}
	return int32(f), nil
}

func IntToFloat(a interface{}) (float32, error) {
	i, ok := a.(int32)
	if !ok {
		return 0, fmt.Errorf("456456 failed to convert argument type %T to int32", a)
	}
	return float32(i), nil
}

func NotInt(a interface{}) (int32, error) {
	v, ok := a.(int32)
	if !ok {
		return 0, fmt.Errorf("failed to convert argument type %T to int32", a)
	}
	if v == 0 {
		v = 1
	} else {
		v = 0
	}
	return v, nil
}

func DropArgs(tt *targetTransform, m []osc.Message) []osc.Message {
	for i := range m {
		m[i].Arguments = nil
	}
	return m
}

func FilterUnselect(tt *targetTransform, m []osc.Message) []osc.Message {
	ret := make([]osc.Message, 0, len(m))
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

func GuessIconAndColour(tt *targetTransform, m []osc.Message) []osc.Message {
	ret := make([]osc.Message, 0, len(m)*3)
	for _, msg := range m {
		if l := len(msg.Arguments); l != 1 {
			glog.Errorf("Expected name message to have 1 arg, got %d", l)
			continue
		}
		name, ok := msg.Arguments[0].(string)
		if !ok {
			glog.Errorf("Expected name message argument to be string, found %T", msg.Arguments[0])
			continue
		}
		ret = append(ret, msg)

		//TODO: guess
		_ = name

	}
	return ret
}

func isArgEq(m osc.Message, idx int, v interface{}) (bool, error) {
	if l := len(m.Arguments); l <= idx {
		return false, fmt.Errorf("message has %d arguments, can't get argument %d", l, idx)
	}
	if aT, bT := reflect.TypeOf(m.Arguments[idx]), reflect.TypeOf(v); aT != bT {
		return false, fmt.Errorf("argument type %s != value type %s", aT, bT)
	}
	return reflect.DeepEqual(m.Arguments[idx], v), nil
}

func (tt *targetTransform) Apply(m mapping, msg osc.Message) ([]osc.Message, error) {
	return tt.transform(tt, m, msg)
}

var (
	// reaperX32StripMap is a map of all /track/${ID}/... subaddresses which
	// are sent by Reaper, and their corresponding X32 targets.
	reaperX32StripMap = map[string]targetTransform{
		"volume": targetTransform{target: "mix/fader", targetType: x32Strip,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mute": targetTransform{target: "mix/on", targetType: x32Strip,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				f, ok := msg.Arguments[0].(float32)
				if !ok {
					return nil, fmt.Errorf("argument 0 is a %T, expected float32", msg.Arguments[0])
				}
				ni, err := NotInt(interface{}(int32(f)))
				if err != nil {
					return nil, err
				}
				msg.Arguments[0] = ni
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"pan": targetTransform{target: "mix/pan", targetType: x32Strip,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"select": targetTransform{target: "-stat/selidx", targetType: x32StatWithAddressArg,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				eq, err := isArgEq(msg, 0, float32(0))
				if err != nil {
					return nil, err
				}
				if eq {
					return []osc.Message{}, nil
				}
				msg.Arguments = []interface{}{int32(m.x32StatIndex)}
				msg.Address = fmt.Sprintf("/%s", tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"solo": targetTransform{target: "-stat/solosw", targetType: x32Stat,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("got %d arguments, expected 1", l)
				}
				f, ok := msg.Arguments[0].(float32)
				if !ok {
					return nil, fmt.Errorf("got %T arg, expected float32", msg.Arguments[0])
				}
				msg.Arguments[0] = int32(f)
				msg.Address = fmt.Sprintf("/%s/%02d", tt.target, m.x32StatIndex)
				return []osc.Message{msg}, nil
			},
		},
		"name": targetTransform{target: "config/name", targetType: x32Strip,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				r := make([]osc.Message, 0, 3)
				// pass on name setting
				r = append(r,
					osc.Message{
						Address:   fmt.Sprintf("/%s/%02d", m.x32Prefix, m.x32StatIndex),
						Arguments: msg.Arguments,
					})

				// do guessing thing
				return r, nil
			},
		},
	}

	// x32eaperStripMap is a map of all addresses which
	// are sent by Reaper, and their corresponding X32 targets.
	x32ReaperStripMap = map[string]targetTransform{
		"mix/fader": targetTransform{target: "volume", targetType: reaperTrack,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mix/on": targetTransform{target: "mute", targetType: reaperTrack,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				i, err := NotInt(msg.Arguments[0])
				if err != nil {
					return nil, err
				}
				f, err := IntToFloat(i)
				if err != nil {
					return nil, err
				}
				msg.Arguments[0] = f
				if m.reaperPrefix != "master" {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				} else {
					msg.Address = "/action/18"
					msg.Arguments[0] = i
				}
				return []osc.Message{msg}, nil
			},
		},
		"mix/pan": targetTransform{target: "pan", targetType: reaperTrack,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperStatMap = map[string]targetTransform{
		"-stat/solosw": targetTransform{target: "solo", targetType: reaperTrack,
			transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("unexpected number of arguments (%d), expected 1", l)
				}
				i, ok := msg.Arguments[0].(int32)
				if !ok {
					return nil, fmt.Errorf("got unexpected argument type %T", msg.Arguments[0])
				}
				bits := strings.Split(msg.Address, "/")
				if l := len(bits); l != 4 {
					return nil, fmt.Errorf("unexpected address format (%s), found %d parts", msg.Address, l)
				}
				statID, err := strconv.Atoi(bits[3])
				if err != nil {
					return nil, fmt.Errorf("failed to parse statID from address[2] (%s): %s", msg.Address, err)
				}
				statID--
				msg.Arguments[0] = float32(i)
				time.Sleep(time.Second)
				msg.Address = fmt.Sprintf("/%s/%s", tt.trackMap.x32StatIndex[int32(statID)].reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperFanoutStatMap = map[string]targetTransform{
		"-stat/selidx": targetTransform{target: "select", targetType: reaperTrackFromArg,
			transform: func(tt *targetTransform, _ mapping, msg osc.Message) ([]osc.Message, error) {
				id, ok := msg.Arguments[0].(int32)
				if !ok {
					return []osc.Message{}, fmt.Errorf("got non-int32 fanout arg[0] of type %T", msg.Arguments[0])
					return []osc.Message{}, nil
				}
				mapping, ok := tt.trackMap.x32StatIndex[id]
				if !ok {
					glog.Errorf("ignoring selidx for unmapped id %d", id)
					return []osc.Message{}, nil
				}
				return []osc.Message{
					// unselect all repaer tracks:
					osc.Message{Address: fmt.Sprintf("/action/40297")},
					// select track matching x32:
					osc.Message{Address: fmt.Sprintf("/%s/%s", mapping.reaperPrefix, tt.target), Arguments: []interface{}{int32(1)}},
				}, nil
			},
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

func (p *Proxy) sendMessagesToX32(msgs []osc.Message) {
	for _, m := range msgs {
		glog.V(1).Infof("->X %s %v", m.Address, m.Arguments)
		if err := p.x32Client.Send(&m); err != nil {
			glog.Errorf("Failed to send message to X32: %s", err)
		}
	}
}

func (p *Proxy) sendMessagesToReaper(msgs []osc.Message) {
	for _, m := range msgs {
		glog.V(1).Infof("->R %s %v", m.Address, m.Arguments)
		if err := p.reaperClient.Send(&m); err != nil {
			glog.Errorf("Failed to send message to reaper: %s", err)
		}
	}
}

func (p *Proxy) buildReaperDispatcher(d *osc.OscDispatcher) error {
	for rPfx, mapping := range p.trackMap.reaper {
		rPfx := rPfx
		mapping := mapping
		for rSfx, tt := range reaperX32StripMap {
			rSfx := rSfx
			tt := tt
			tt.trackMap = &p.trackMap

			reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
			d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
				glog.V(1).Infof("R-> %s %v", reaAddr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", reaAddr, err)
					return
				}
				p.sendMessagesToX32(msgs)
			})
		}
	}
	return nil
}

func (p *Proxy) buildX32Dispatcher(d *osc.OscDispatcher) error {
	for xPfx, mapping := range p.trackMap.x32 {
		xPfx := xPfx
		mapping := mapping

		// Track addresses
		for xSfx, tt := range x32ReaperStripMap {
			xSfx := xSfx
			tt := tt
			tt.trackMap = &p.trackMap

			x32Addr := fmt.Sprintf("/%s/%s", xPfx, xSfx)
			d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
					return
				}
				p.sendMessagesToReaper(msgs)
			})
		}

		// Stat addresses
		for xStat, tt := range x32ReaperStatMap {
			xStat := xStat
			tt := tt
			tt.trackMap = &p.trackMap

			x32Addr := fmt.Sprintf("/%s/%02d", xStat, mapping.x32StatIndex)
			d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				if msg.Address == "/-stat/solo" {
					return
				}
				glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
					return
				}
				p.sendMessagesToReaper(msgs)
			})
		}
	}

	// Fan-out stat
	for xStat, tt := range x32ReaperFanoutStatMap {
		xStat := xStat
		tt := tt
		tt.trackMap = &p.trackMap

		x32Addr := fmt.Sprintf("/%s", xStat)
		d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
			glog.V(1).Infof("X-> %s %v", x32Addr, msg.Arguments)
			msgs, err := tt.Apply(mapping{}, *msg)
			if err != nil {
				glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
				return
			}
			p.sendMessagesToReaper(msgs)
		})
	}

	d.AddMsgHandler("", func(msg *osc.Message) {
		glog.V(1).Infof("Unhandled X-> %s %v", msg.Address, msg.Arguments)
	})
	return nil
}
