package x32

import (
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/9600org/go-osc/osc"
	"github.com/golang/glog"
	"golang.org/x/sync/errgroup"
)

type ProxyConfig struct {
	ListenAddress string `yaml:"listenAddress"`
	ReaperAddress string `yaml:"reaperAddress"`
	X32Address    string `yaml:"x32Address"`

	Mapping map[string]string `yaml:"mapping"`

	NameHints map[string]string `yaml:"nameHints"`

	FxMap  map[string]map[string]string `yaml:"fxMap"`
	FxType string                       `yaml:"fxType"`
}

type Proxy struct {
	reaperServer *osc.Server
	reaperClient Client
	x32Server    *osc.Server
	x32Client    Client
	x32ServeConn net.PacketConn

	state state

	nameHints         []nameHint
	fxPlugName        string
	fxParamNameLookup map[string]string

	plugTypes map[string]*plugParams

	dispatcher osc.Dispatcher
}

type state struct {
	trackMap      trackMap
	selectedTrack *mapping
}

type trackMap struct {
	reaper       map[string]*mapping
	x32          map[string]*mapping
	x32StatIndex map[int32]*mapping
}

type mapping struct {
	reaperPrefix     string
	reaperTrackIndex int32
	fxMap            *fxMap
	x32Prefix        string
	x32StatIndex     int32
}

type plugParams struct {
	plugName string

	// EQ Param indices for bands 0..[4,6]
	eqTypeIndex   []int32
	eqFreqIndex   []int32
	eqGainIndex   []int32
	eqQIndex      []int32
	eqEnableIndex []int32
}

type fxMap struct {
	// FX indices
	reaEqIndex   int32 // index of fx which handles EQ
	reaGateIndex int32
	reaDynIndex  int32

	plugParams *plugParams

	// Gate Param indices

	// Dyn Param indices
}

var colours = map[string]int32{
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
	icon    int32
	colour  int32
}

func newNameHint(m, hint string) (*nameHint, error) {
	ret := &nameHint{nil, -1, -1}
	var err error
	ret.matcher, err = regexp.Compile(strings.ToLower(m))
	if err != nil {
		return nil, err
	}
	hintBits := strings.Split(hint, " ")
	if len(hintBits) >= 1 {
		icon, err := strconv.Atoi(hintBits[0])
		if err != nil {
			return nil, err
		}
		ret.icon = int32(icon)
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
		reaper:       make(map[string]*mapping),
		x32:          make(map[string]*mapping),
		x32StatIndex: make(map[int32]*mapping),
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
			mapping := &mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: k, reaperTrackIndex: -1}
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
			mapping := &mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: reaK, reaperTrackIndex: reaN}
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
	xServerConn.SetReadBuffer(1 << 20)
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
	rClientConn.SetReadBuffer(1 << 20)
	rClient := Client{rClientConn}

	glog.V(1).Infof("Building maps")

	trackMap, err := buildTrackMap(config.Mapping)
	if err != nil {
		return nil, err
	}

	nameHints, err := buildNameHints(config.NameHints)
	if err != nil {
		return nil, err
	}

	// TODO support multiple fx types - e.g. different plugs for different fx
	fxType := config.FxType
	fxMap := config.FxMap[fxType]
	paramLookup := buildParamLookup(fxMap)

	p := &Proxy{
		reaperServer:      reaperServer,
		reaperClient:      rClient,
		x32Server:         xServer,
		x32Client:         xClient,
		x32ServeConn:      xServerConn,
		state:             state{trackMap: *trackMap},
		nameHints:         nameHints,
		fxPlugName:        fxType,
		fxParamNameLookup: paramLookup,
		plugTypes:         make(map[string]*plugParams),
	}

	return p, nil
}

func buildParamLookup(m map[string]string) map[string]string {
	ret := make(map[string]string)
	for k, v := range m {
		ret[v] = k
	}
	return ret
}

func (p *Proxy) ListenAndServe() error {
	done := make(chan struct{})
	defer close(done)

	glog.V(1).Infof("Building Reaper dispatcher")
	p.reaperServer.Dispatcher = NewExactDispatcher()
	p.buildReaperDispatcher(p.reaperServer.Dispatcher)

	glog.V(1).Infof("Building X32 dispatcher")
	p.x32Server.Dispatcher = NewExactDispatcher()
	p.buildX32Dispatcher(p.x32Server.Dispatcher)

	glog.V(1).Infof("Starting server")

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

	p.sendMessagesToReaper([]osc.Message{
		osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
	})

	errGroup := errgroup.Group{}
	errGroup.Go(func() error {
		return p.x32Server.Serve(p.x32ServeConn)
	})

	errGroup.Go(p.reaperServer.ListenAndServe)

	return errGroup.Wait()
}

type targetTransform struct {
	target    string
	transform func(*targetTransform, *mapping, osc.Message) ([]osc.Message, error)
	state     *state
	nameHints []nameHint
	fxIndex   int32
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

func isArgEq(m osc.Message, idx int, v interface{}) (bool, error) {
	if l := len(m.Arguments); l <= idx {
		return false, fmt.Errorf("message has %d arguments, can't get argument %d", l, idx)
	}
	if aT, bT := reflect.TypeOf(m.Arguments[idx]), reflect.TypeOf(v); aT != bT {
		return false, fmt.Errorf("argument type %s != value type %s", aT, bT)
	}
	return reflect.DeepEqual(m.Arguments[idx], v), nil
}

func (tt *targetTransform) Apply(m *mapping, msg osc.Message) ([]osc.Message, error) {
	return tt.transform(tt, m, msg)
}

func getIntArg(m osc.Message, i int) (int32, error) {
	if l := len(m.Arguments); l <= i {
		return 0, fmt.Errorf("insufficient args (%d), wanted > %d", l, i)
	}
	r, ok := m.Arguments[i].(int32)
	if !ok {
		return 0, fmt.Errorf("got arg type %T, wanted int32)", m.Arguments[i])
	}
	return r, nil
}

func getFloatArg(m osc.Message, i int) (float32, error) {
	if l := len(m.Arguments); l <= i {
		return 0, fmt.Errorf("insufficient args (%d), wanted > %d", l, i)
	}
	r, ok := m.Arguments[i].(float32)
	if !ok {
		return 0, fmt.Errorf("got arg type %T, wanted float32)", m.Arguments[i])
	}
	return r, nil
}

// guessIconAndColour uses the configured name hints to guess at appropriate
// icons and colours for the X32 scribble strip based on the passed in name.
func guessColourAndIcon(nameHints []nameHint, name string) (int32, int32) {
	colID := int32(-1)
	iconID := int32(-1)
	if len(strings.TrimSpace(name)) == 0 {
		colID = 0
		iconID = 0
	} else {
		for _, hint := range nameHints {
			if hint.matcher.Match([]byte(strings.ToLower(name))) {
				if colID == -1 && hint.colour > -1 {
					colID = hint.colour
				}
				if iconID == -1 && hint.icon > -1 {
					iconID = hint.icon
				}
				if colID > -1 && iconID > -1 {
					break
				}
			}
		}
	}
	return colID, iconID
}

var (
	// reaperX32StripMap is a map of all /track/${ID}/... subaddresses which
	// are sent by Reaper, and their corresponding X32 targets.
	reaperX32StripMap = map[string]targetTransform{
		"volume": targetTransform{target: "mix/fader",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mute": targetTransform{target: "mix/on",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
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
		"pan": targetTransform{target: "mix/pan",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"select": targetTransform{target: "-stat/selidx",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				eq, err := isArgEq(msg, 0, float32(0))
				if err != nil {
					return nil, err
				}
				if eq {
					return []osc.Message{}, nil
				}
				defer func() {
					tt.state.selectedTrack = m
				}()
				msgs := []osc.Message{
					// select track on X32
					osc.Message{
						Address:   fmt.Sprintf("/%s", tt.target),
						Arguments: []interface{}{int32(m.x32StatIndex)},
					},
					// Ping reaper to send FX info
					//TODO: broken - this send to x32...
					/*
						osc.Message{
							Address: fmt.Sprintf("/device/track/select"),
							Arguments: []interface{}{int32(m.reaperTrackIndex)},
						},
						osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
					*/
				}
				return msgs, nil
			},
		},
		"solo": targetTransform{target: "-stat/solosw",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
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
		"name": targetTransform{target: "config/name",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				name, ok := msg.Arguments[0].(string)
				if !ok {
					return nil, fmt.Errorf("got %T arg, expected string", msg.Arguments[0])
				}

				r := make([]osc.Message, 0, 3)

				colID, iconID := guessColourAndIcon(tt.nameHints, name)
				if colID > -1 {
					r = append(r, osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, "config/color"),
						Arguments: []interface{}{colID},
					})
				}
				if iconID > -1 {
					r = append(r, osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, "config/icon"),
						Arguments: []interface{}{iconID},
					})
				}

				// pass on name setting
				r = append(r,
					osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target),
						Arguments: msg.Arguments,
					})

				// do guessing thing
				return r, nil
			},
		},
	}

	// reaperX32StripFXMap contains mappings for reaper FX messages
	reaperX32StripFXMap = map[string]targetTransform{
		// General purpose EQ plugins
		// TODO fx index management
		/*
			"fx/1/fxparam/%d/bypass": targetTransform{target: "fx/%d/eq/on",
				transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
					if l := len(msg.Arguments); l != 1 {
						return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
					}
					i, ok := msg.Arguments[0].(int32)
					if !ok {
						return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
					}
					msg.Arguments[0] = i ^ 1
					msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
		*/
		"fx/[1-5]/fxparam/%d/value": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"fxeq/band/%d/q/oct": targetTransform{target: "eq/%d/q",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"fxeq/band/%d/f/hz": targetTransform{target: "eq/%d/f",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"fxeq/band/%d/g/db": targetTransform{target: "eq/%d/g",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		/*
				// TODO: ReaEQ
			"fxeq/band/%d/bypass": targetTransform{target: "fx/%d/eq/on",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					if l := len(msg.Arguments); l != 1 {
						return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
					}
					i, ok := msg.Arguments[0].(int32)
					if !ok {
						return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
					}
					msg.Arguments[0] = i ^ 1
					msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/type": targetTransform{target: "eq/%d/type",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/q/oct": targetTransform{target: "eq/%d/q",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/f/hz": targetTransform{target: "eq/%d/f",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/g/db": targetTransform{target: "eq/%d/g",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
		*/
	}

	// x32eaperStripMap is a map of all addresses which
	// are sent by Reaper, and their corresponding X32 targets.
	x32ReaperStripMap = map[string]targetTransform{
		"mix/fader": targetTransform{target: "volume",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mix/on": targetTransform{target: "mute",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
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
		"mix/pan": targetTransform{target: "pan",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	// x32ReaperStripFXMap contains mappings for x32 FX messages
	x32ReaperStripFXMap = map[string]targetTransform{
		"eq/%d/on": targetTransform{target: "fxeq/band/%d/bypass",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
				}
				i, ok := msg.Arguments[0].(int32)
				if !ok {
					return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
				}
				msg.Arguments[0] = i ^ 1
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/type": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if m.fxMap == nil {
					return nil, fmt.Errorf("fxmap nil")

				}
				if m.fxMap.plugParams == nil {
					return nil, fmt.Errorf("plugParams nil")
				}
				x32EqType, err := getIntArg(msg, 0)
				if err != nil {
					return nil, err
				}
				plugEqType := float32(0)
				// TODO: make this configurable - assumes Neutron currently
				switch x32EqType {
				case 0:
					plugEqType = float32(11.0) / 12
				case 1:
					plugEqType = float32(6.0) / 12
				case 2:
					plugEqType = float32(0.0) / 12
				case 3:
					plugEqType = float32(2.0) / 12
				case 4:
					plugEqType = float32(8.0) / 12
				case 5:
					plugEqType = float32(9.0) / 12
				}

				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqTypeIndex[tt.fxIndex])
				msg.Arguments = []interface{}{plugEqType}
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/q": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqQIndex[tt.fxIndex])
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/f": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqFreqIndex[tt.fxIndex])
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/g": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqGainIndex[tt.fxIndex])
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperStatMap = map[string]targetTransform{
		"-stat/solosw": targetTransform{target: "solo",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
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
				msg.Address = fmt.Sprintf("/%s/%s", tt.state.trackMap.x32StatIndex[int32(statID)].reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperFanoutStatMap = map[string]targetTransform{
		"-stat/selidx": targetTransform{target: "select",
			transform: func(tt *targetTransform, _ *mapping, msg osc.Message) ([]osc.Message, error) {
				id, ok := msg.Arguments[0].(int32)
				if !ok {
					return []osc.Message{}, fmt.Errorf("got non-int32 fanout arg[0] of type %T", msg.Arguments[0])
					return []osc.Message{}, nil
				}
				mapping, ok := tt.state.trackMap.x32StatIndex[id]
				if !ok {
					glog.Errorf("ignoring selidx for unmapped id %d", id)
					return []osc.Message{}, nil
				}
				defer func() {
					tt.state.selectedTrack = mapping
				}()
				msgs := []osc.Message{
					// unselect all repaer tracks:
					osc.Message{Address: fmt.Sprintf("/action/40297")},
					// select track matching x32:
					osc.Message{Address: fmt.Sprintf("/%s/%s", mapping.reaperPrefix, tt.target), Arguments: []interface{}{int32(1)}},
					// Ping reaper to send FX info
					osc.Message{Address: "/device/track/select", Arguments: []interface{}{mapping.reaperTrackIndex}},
					osc.Message{Address: "/device/fx/select", Arguments: []interface{}{int32(1)}},
					//osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
				}
				return msgs, nil
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

// FX:
// /ch/01/eq/1/type i
// /ch/01/eq/1/g f
// /ch/01/eq/1/q f
// /ch/01/eq/1/f f

func (p *Proxy) sendMessagesToX32(msgs []osc.Message) {
	for _, m := range msgs {
		glog.V(2).Infof("->X %s %v", m.Address, m.Arguments)
		if err := p.x32Client.Send(&m); err != nil {
			glog.Errorf("Failed to send message to X32: %s", err)
		}
	}
}

func (p *Proxy) sendMessagesToReaper(msgs []osc.Message) {
	for _, m := range msgs {
		glog.V(2).Infof("->R %s %v", m.Address, m.Arguments)
		if err := p.reaperClient.Send(&m); err != nil {
			glog.Errorf("Failed to send message to reaper: %s", err)
		}
	}
}

func (p *Proxy) buildReaperDispatcher(d osc.Dispatcher) error {
	for rPfx, mapping := range p.state.trackMap.reaper {
		rPfx := rPfx
		mapping := mapping
		for rSfx, tt := range reaperX32StripMap {
			rSfx := rSfx
			tt := tt
			tt.state = &p.state
			tt.nameHints = p.nameHints

			reaAddr := fmt.Sprintf("/%s/%s", rPfx, rSfx)
			d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
				glog.V(2).Infof("R-> %s %v", reaAddr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", reaAddr, err)
					return
				}
				p.sendMessagesToX32(msgs)
			})
		}
		for fxIdx := int32(0); fxIdx < 10; fxIdx++ {
			fxIdx := fxIdx
			fxNameAddr := fmt.Sprintf("/%s/fx/%d/name", rPfx, fxIdx)
			if err := d.AddMsgHandler(fxNameAddr, func(msg *osc.Message) {
				name, ok := msg.Arguments[0].(string)
				if !ok || len(name) == 0 {
					return
				}
				if strings.Contains(name, p.fxPlugName) {
					pt := p.plugTypes[name]
					if pt == nil {
						pt = &plugParams{
							plugName:    name,
							eqTypeIndex: make([]int32, 6),
							eqFreqIndex: make([]int32, 6),
							eqGainIndex: make([]int32, 6),
							eqQIndex:    make([]int32, 6),
						}

						p.plugTypes[name] = pt
					}
					glog.Infof("Using %q at %d for FX on %s", name, fxIdx, mapping.reaperPrefix)
					//TODO handle different plugins for each FX type
					mapping.fxMap = &fxMap{
						reaEqIndex:   fxIdx,
						reaGateIndex: fxIdx,
						reaDynIndex:  fxIdx,
						plugParams:   pt,
					}
				} else {
					mapping.fxMap = nil
				}
			}); err != nil {
				glog.Infof("failed to register catcher func")
			}
		}
	}

	// FX Name watcher
	catcherFunc := func(m *osc.Message) {
		if p.state.selectedTrack == nil {
			return
		}
		if p.state.selectedTrack.fxMap == nil {
			return
		}
		addrBits := strings.Split(m.Address, "/")
		fxIndex, err := strconv.Atoi(addrBits[4])
		if err != nil {
			glog.Errorf("Failed to parse FX index: %v", err)
			return
		}
		name, _ := m.Arguments[0].(string)
		if len(name) == 0 {
			return
		}
		param, ok := p.fxParamNameLookup[name]
		if !ok {
			return
		}
		bits := strings.Split(param, "/")
		idx := 0
		if len(bits) == 2 {
			i, err := strconv.Atoi(bits[1])
			if err != nil {
				glog.Errorf("invalid fx param index %v: %v", bits[1], err)
				return
			}
			idx = i - 1
		}
		switch bits[0] {
		case "eqFreqName":
			p.state.selectedTrack.fxMap.plugParams.eqFreqIndex[idx] = int32(fxIndex)
		case "eqGainName":
			p.state.selectedTrack.fxMap.plugParams.eqGainIndex[idx] = int32(fxIndex)
		case "eqTypeName":
			p.state.selectedTrack.fxMap.plugParams.eqTypeIndex[idx] = int32(fxIndex)
		case "eqQName":
			p.state.selectedTrack.fxMap.plugParams.eqQIndex[idx] = int32(fxIndex)
		case "gateThresholdName":
		case "gateRangeName":
		case "gateAttackName":
		case "gateHoldName":
		case "gateReleaseName":
		default:
			glog.Errorf("unknown param name %s", bits[0])
			return
		}
		glog.Infof("Found track %d param %s at %d", p.state.selectedTrack.reaperTrackIndex, bits[0], fxIndex)
	}
	for fxIdx := 0; fxIdx < 10; fxIdx++ {
		for paramIdx := 0; paramIdx < 300; paramIdx++ {
			if err := d.AddMsgHandler(fmt.Sprintf("/fx/%d/fxparam/%d/name", fxIdx, paramIdx), catcherFunc); err != nil {
				glog.Infof("failed to register catcher func")
			}
		}

		/*

			for fxAddr, ttFxTemplate := range reaperX32StripFXMap {
				fxAddr := fxAddr

				reaFxAddrTemplate := fmt.Sprintf("/%s/%s", rPfx, fxAddr)
				// TODO: 4 for most things, 6 for bus
				for i := 1; i <= 6; i++ {
					ttFx := ttFxTemplate
					ttFx.target = fmt.Sprintf(ttFx.target, i)
					reaFxAddr := fmt.Sprintf(reaFxAddrTemplate, i)
					err := d.AddMsgHandler(reaFxAddr, func(msg *osc.Message) {
						msgs, err := ttFx.Apply(mapping, *msg)
						if err != nil {
							glog.Errorf("%s: failed to handle message: %v", reaFxAddr, err)
							return
						}
						p.sendMessagesToReaper(msgs)
					})
					if err != nil {
						glog.Errorf("%s: failed to add handler: %v", reaFxAddr, err)
					}
				}
			}
		*/
	}
	return nil
}

func (p *Proxy) buildX32Dispatcher(d osc.Dispatcher) error {
	for xPfx, mapping := range p.state.trackMap.x32 {
		xPfx := xPfx
		mapping := mapping

		// Track addresses
		for xSfx, tt := range x32ReaperStripMap {
			xSfx := xSfx
			tt := tt
			tt.state = &p.state
			tt.nameHints = p.nameHints

			x32Addr := fmt.Sprintf("/%s/%s", xPfx, xSfx)
			d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				glog.V(2).Infof("X-> %s %v", x32Addr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
					return
				}
				p.sendMessagesToReaper(msgs)
			})
		}

		for fxAddr, ttFxTemplate := range x32ReaperStripFXMap {
			fxAddr := fxAddr

			x32FxAddrTemplate := fmt.Sprintf("/%s/%s", xPfx, fxAddr)
			// TODO: 4 for most things, 6 for bus
			for i := int32(1); i <= 6; i++ {
				ttFx := ttFxTemplate
				ttFx.fxIndex = i - 1
				x32FxAddr := fmt.Sprintf(x32FxAddrTemplate, i)
				err := d.AddMsgHandler(x32FxAddr, func(msg *osc.Message) {
					msgs, err := ttFx.Apply(mapping, *msg)
					if err != nil {
						glog.Errorf("%s: failed to handle message: %v", x32FxAddr, err)
						return
					}
					p.sendMessagesToReaper(msgs)
				})
				if err != nil {
					glog.Errorf("%s: failed to add handler: %v", x32FxAddr, err)
				}
			}
		}

		// Stat addresses
		for xStat, tt := range x32ReaperStatMap {
			xStat := xStat
			tt := tt
			tt.state = &p.state
			tt.nameHints = p.nameHints

			x32Addr := fmt.Sprintf("/%s/%02d", xStat, mapping.x32StatIndex)
			d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				if msg.Address == "/-stat/solo" {
					return
				}
				glog.V(2).Infof("X-> %s %v", x32Addr, msg.Arguments)
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
		tt.state = &p.state
		tt.nameHints = p.nameHints

		x32Addr := fmt.Sprintf("/%s", xStat)
		d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
			glog.V(2).Infof("X-> %s %v", x32Addr, msg.Arguments)
			msgs, err := tt.Apply(&mapping{}, *msg)
			if err != nil {
				glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
				return
			}
			p.sendMessagesToReaper(msgs)
		})
	}

	d.AddMsgHandler("/", func(msg *osc.Message) {
		glog.V(2).Infof("Unhandled X-> %s %v", msg.Address, msg.Arguments)
	})
	return nil
}
