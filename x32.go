package x32

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
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
	dispatcher   osc.Dispatcher

	fxPlugName        string
	fxParamNameLookup map[string]string
	nameHints         []nameHint

	_state state
}

type state struct {
	mu            *sync.RWMutex // protects everything below
	trackMap      trackMap
	selectedTrack *mapping
	plugParams    map[string]*plugParams
}

func (s *state) getReaperPrefixForX32StatIndex(i int) (mapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.trackMap.x32StatIndex[int32(i)]
	return *m, ok

}

func (s *state) getPlugParams(name string) (*plugParams, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plugParams[name]
	return p, ok
}

func (s *state) setPlugParams(name string, pp *plugParams) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plugParams[name] = pp
}

func (s *state) getTrackMappingForReaper(name string) (mapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.trackMap.reaper[name]
	return *m, ok
}

func (s *state) setTrackMappingForReaper(name string, m mapping) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackMap.reaper[name] = &m
}

func (s *state) getTrackMappingForX32(name string) (mapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.trackMap.x32[name]
	return *m, ok
}

func (s *state) setTrackMappingForX32(name string, m mapping) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackMap.x32[name] = &m
}

func (s *state) getTrackMappingForX32StatID(id int32) (mapping, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.trackMap.x32StatIndex[id]
	return *m, ok
}

func (s *state) setTrackMappingForX32StatID(id int32, m mapping) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackMap.x32StatIndex[id] = &m
}

func (s *state) getSelectedTrack() *mapping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectedTrack
}

func (s *state) selectTrackX32StatID(id int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedTrack = s.trackMap.x32StatIndex[id]
}
func (s *state) removeFx(reaperPrefix string, fxIndex int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.trackMap.reaper[reaperPrefix]
	if !ok {
		return
	}
	m.fxMap = nil
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
		nameHints:         nameHints,
		fxPlugName:        fxType,
		fxParamNameLookup: paramLookup,
		_state: state{
			mu:         &sync.RWMutex{},
			trackMap:   *trackMap,
			plugParams: make(map[string]*plugParams),
		},
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

func (p *Proxy) sendXremote() {
	if err := p.x32Client.Send(osc.NewMessage("/xremote")); err != nil {
		glog.Warningf("Failed to send xremote: %v", err)
	}
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

	// Spin off xremote pinger
	go func() {
		t := time.NewTicker(5 * time.Second)

		for {
			select {
			case <-t.C:
				p.sendXremote()
			case <-done:
				return
			}
		}
	}()

	p.sendXremote()

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

// nameHandler is a callback for dealing with track name messages from Reaper.
func (p *Proxy) nameHandler(m *osc.Message) {
	selTrack := p._state.getSelectedTrack()
	if selTrack == nil {
		return
	}
	if selTrack.fxMap == nil {
		return
	}
	addrBits := strings.Split(m.Address, "/")
	paramIndex, err := strconv.Atoi(addrBits[4])
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
	x32BandIndex := int32(0)
	if len(bits) == 2 {
		i, err := strconv.Atoi(bits[1])
		if err != nil {
			glog.Errorf("invalid fx param index %v: %v", bits[1], err)
			return
		}
		x32BandIndex = int32(i - 1)
	}

	fxMap := selTrack.fxMap
	pi32 := int32(paramIndex)
	switch bits[0] {
	case "eqFreqName":
		fxMap.setEqPlugBandFreqParam(x32BandIndex, pi32)
		fxMap.setEqParamInfo(pi32, paramInfo{
			x32AddrFormat: fmt.Sprintf("eq/%d/f", x32BandIndex+1),
			normToX32:     hzToX32EqFreq,
			plugToNorm:    neutronEqLogToHz,
		})
	case "eqGainName":
		fxMap.setEqPlugBandGainParam(x32BandIndex, pi32)
		fxMap.setEqParamInfo(pi32, paramInfo{
			x32AddrFormat: fmt.Sprintf("eq/%d/g", x32BandIndex+1),
			normToX32:     func(g float32) float32 { return g },
			plugToNorm:    neutronToNormGain,
		})
	case "eqTypeName":
		fxMap.setEqPlugBandTypeParam(x32BandIndex, pi32)
		fxMap.setEqParamInfo(pi32, paramInfo{
			x32AddrFormat: fmt.Sprintf("eq/%d/type", x32BandIndex+1),
			normToX32:     func(g float32) float32 { return g },
			plugToNorm:    neutronEqTypeToX32,
			format:        func(a interface{}) interface{} { return int32(a.(float32)) },
		})
	case "eqQName":
		fxMap.setEqPlugBandQParam(x32BandIndex, pi32)
		fxMap.setEqParamInfo(pi32, paramInfo{
			x32AddrFormat: fmt.Sprintf("eq/%d/q", x32BandIndex+1),
			normToX32:     octToX32Q,
			plugToNorm:    neutronQLogToOct,
		})
	case "gateThresholdName":
	case "gateRangeName":
	case "gateAttackName":
	case "gateHoldName":
	case "gateReleaseName":
	default:
		glog.Errorf("unknown param name %s", bits[0])
		return
	}
	glog.V(2).Infof("Found track %d param %s at %d", selTrack.reaperTrackIndex, bits[0], pi32)
}

func (p *Proxy) buildReaperDispatcher(d osc.Dispatcher) error {
	p._state.mu.Lock()
	defer p._state.mu.Unlock()

	for rPfx, mapping := range p._state.trackMap.reaper {
		rPfx := rPfx
		mapping := mapping
		for rSfx, tt := range reaperX32StripMap {
			rSfx := rSfx
			tt := tt
			tt.state = &p._state
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
				if !ok {
					return
				}
				if len(name) == 0 {
					//TODO handle different plugins for each FX type
					p._state.removeFx(rPfx, fxIdx)
					return
				}
				if strings.Contains(name, p.fxPlugName) {
					pt, ok := p._state.getPlugParams(name)
					if !ok {
						pt = &plugParams{
							plugName:        name,
							eqTypeBandParam: make([]int32, 6),
							eqFreqBandParam: make([]int32, 6),
							eqGainBandParam: make([]int32, 6),
							eqQBandParam:    make([]int32, 6),
							eqParamInfo:     make(map[int32]paramInfo),
							// TODO: make this configurable - assumes Neutron currently
							eqFreqToPlug:   hzToNeutronEqLog,
							eqFreqFromPlug: neutronEqLogToHz,
							eqGainToPlug:   normToNeutronGain,
							eqGainFromPlug: neutronToNormGain,
							eqQToPlug:      octToNeutronQLog,
							eqQFromPlug:    neutronQLogToOct,
							eqTypeToPlug:   x32EqTypeToNeutron,
							eqTypeFromPlug: neutronEqTypeToX32,
						}

						p._state.setPlugParams(name, pt)
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
			for fxAddr, ttFxTemplate := range reaperX32StripFXMap {
				fxAddr := fxAddr

				reaFxAddrTemplate := fmt.Sprintf("/%s/%s", rPfx, fxAddr)
				// TODO: 4 for most things, 6 for bus
				for paramIdx := 1; paramIdx <= 300; paramIdx++ {
					ttFx := ttFxTemplate
					ttFx.state = &p._state
					ttFx.nameHints = p.nameHints
					//ttFx.target = fmt.Sprintf(ttFx.target, i)

					reaFxAddr := fmt.Sprintf(reaFxAddrTemplate, fxIdx, paramIdx)
					err := d.AddMsgHandler(reaFxAddr, func(msg *osc.Message) {
						msgs, err := ttFx.Apply(mapping, *msg)
						if err != nil {
							glog.Errorf("%s: failed to handle message: %v", reaFxAddr, err)
							return
						}
						p.sendMessagesToX32(msgs)
					})
					if err != nil {
						glog.Errorf("%s: failed to add handler: %v", reaFxAddr, err)
					}
				}
			} // fxIdx
		}
	}

	for fxIdx := 0; fxIdx < 10; fxIdx++ {
		for paramIdx := 0; paramIdx < 300; paramIdx++ {
			if err := d.AddMsgHandler(fmt.Sprintf("/fx/%d/fxparam/%d/name", fxIdx, paramIdx), p.nameHandler); err != nil {
				glog.Infof("failed to register catcher func")
			}
		}
	}
	return nil
}

func (p *Proxy) buildX32Dispatcher(d osc.Dispatcher) error {
	p._state.mu.Lock()
	defer p._state.mu.Unlock()

	for xPfx, mapping := range p._state.trackMap.x32 {
		xPfx := xPfx
		mapping := mapping

		// Track addresses
		for xSfx, tt := range x32ReaperStripMap {
			xSfx := xSfx
			tt := tt
			tt.state = &p._state
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
			tt.state = &p._state
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
		tt.state = &p._state
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
