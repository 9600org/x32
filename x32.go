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

	FxMap  map[string]map[string]map[string]int32 `yaml:"fxMap"`
	FxType string                                 `yaml:"fxType"`
}

type Proxy struct {
	reaperServer *osc.Server
	reaperClient Client
	x32Server    *osc.Server
	x32Client    Client
	x32ServeConn net.PacketConn
	dispatcher   osc.Dispatcher

	fxPlugName   string
	fxPlugParams map[string]plugParams
	nameHints    []nameHint

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
	if m.fxMap.eq != nil && m.fxMap.eq.vstIndex == fxIndex {
		glog.V(1).Infof("Removing EQ mapping at %s@%d", reaperPrefix, fxIndex)
		m.fxMap.eq = nil
	}
	if m.fxMap.gate != nil && m.fxMap.gate.vstIndex == fxIndex {
		glog.V(1).Infof("Removing Gate mapping at %s@%d", reaperPrefix, fxIndex)
		m.fxMap.gate = nil
	}
	if m.fxMap.dyn != nil && m.fxMap.dyn.vstIndex == fxIndex {
		glog.V(1).Infof("Removing Dyn mapping at %s@%d", reaperPrefix, fxIndex)
		m.fxMap.dyn = nil
	}
}

type trackMap struct {
	reaper       map[string]*mapping
	x32          map[string]*mapping
	x32StatIndex map[int32]*mapping
}

type mapping struct {
	reaperPrefix     string
	reaperTrackIndex int32
	fxMap            fxMap
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
	pp, err := buildPlugParamsMap(config.FxMap)
	if err != nil {
		return nil, err
	}

	p := &Proxy{
		reaperServer: reaperServer,
		reaperClient: rClient,
		x32Server:    xServer,
		x32Client:    xClient,
		x32ServeConn: xServerConn,
		nameHints:    nameHints,
		fxPlugName:   fxType,
		fxPlugParams: pp,
		_state: state{
			mu:         &sync.RWMutex{},
			trackMap:   *trackMap,
			plugParams: make(map[string]*plugParams),
		},
	}

	return p, nil
}

func buildPlugParamsMap(m map[string]map[string]map[string]int32) (map[string]plugParams, error) {
	ret := make(map[string]plugParams)
	for fxName, sections := range m {
		pt := plugParams{
			plugName:        fxName,
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
		for secName, secParams := range sections {
			switch secName {
			case "eq":
				for param, paramIndex := range secParams {
					bits := strings.Split(param, "/")
					if len(bits) != 2 {
						return nil, fmt.Errorf("expected EQ FX param name format: type/[1-6]")
						continue
					}
					eqIdx, err := strconv.Atoi(bits[1])
					if err != nil {
						return nil, err
					}
					if eqIdx < 1 || eqIdx > 6 {
						return nil, fmt.Errorf("eq band %d out of range [1-6] in %s", eqIdx, fxName)
					}
					eqIdx-- // Adjust to zero-based
					switch strings.ToLower(bits[0]) {
					case "freq":
						pt.eqFreqBandParam[eqIdx] = paramIndex
						pt.eqParamInfo[paramIndex] = paramInfo{
							x32AddrFormat: fmt.Sprintf("eq/%d/f", eqIdx+1),
							normToX32:     hzToX32EqFreq,
							plugToNorm:    neutronEqLogToHz,
						}
					case "gain":
						pt.eqGainBandParam[eqIdx] = paramIndex
						pt.eqParamInfo[paramIndex] = paramInfo{
							x32AddrFormat: fmt.Sprintf("eq/%d/g", eqIdx+1),
							normToX32:     func(g float32) float32 { return g },
							plugToNorm:    neutronToNormGain,
						}
					case "q":
						pt.eqQBandParam[eqIdx] = paramIndex
						pt.eqParamInfo[paramIndex] = paramInfo{
							x32AddrFormat: fmt.Sprintf("eq/%d/type", eqIdx+1),
							normToX32:     func(g float32) float32 { return g },
							plugToNorm:    neutronEqTypeToX32,
							format:        func(a interface{}) interface{} { return int32(a.(float32)) },
						}
					case "type":
						pt.eqTypeBandParam[eqIdx] = paramIndex
						pt.eqParamInfo[paramIndex] = paramInfo{
							x32AddrFormat: fmt.Sprintf("eq/%d/q", eqIdx+1),
							normToX32:     octToX32Q,
							plugToNorm:    neutronQLogToOct,
						}
					default:
						return nil, fmt.Errorf("unknown eq param name %s in fxmap %s", bits[0], fxName)
					}
				}
			case "gain":
			case "dyn":
			default:
				return nil, fmt.Errorf("unknown section name %s in fxmap %s", secName, fxName)
			}
		}
		ret[fxName] = pt
	}
	return ret, nil
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
	p.configureReaperDispatcher(p.reaperServer.Dispatcher)

	glog.V(1).Infof("Building X32 dispatcher")
	p.x32Server.Dispatcher = NewExactDispatcher()
	p.configureX32Dispatcher(p.x32Server.Dispatcher)

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

	errGroup := errgroup.Group{}
	errGroup.Go(func() error {
		return p.x32Server.Serve(p.x32ServeConn)
	})

	errGroup.Go(p.reaperServer.ListenAndServe)
	errGroup.Go(func() error { return ListenAndServeStatus(":8000", &p._state) })

	time.Sleep(time.Second) // TODO: yuk

	p.sendMessagesToReaper([]osc.Message{
		osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
	})

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

// configureReaperDispatcher configures a Dispatcher with registrations for all
// Reaper originated OSC messages that we're interested in.
func (p *Proxy) configureReaperDispatcher(d osc.Dispatcher) error {
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
			if err := d.AddMsgHandler(reaAddr, func(msg *osc.Message) {
				glog.V(2).Infof("R-> %s %v", reaAddr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", reaAddr, err)
					return
				}
				p.sendMessagesToX32(msgs)
			}); err != nil {
				return err
			}
		}
		for fxIdx := int32(0); fxIdx < 10; fxIdx++ {
			fxIdx := fxIdx
			fxNameAddr := fmt.Sprintf("/%s/fx/%d/name", rPfx, fxIdx)
			if err := d.AddMsgHandler(fxNameAddr, func(msg *osc.Message) {
				name, ok := msg.Arguments[0].(string)
				if !ok {
					return
				}
				name = strings.TrimSpace(name)
				if len(name) == 0 {
					//TODO handle different plugins for each FX type
					p._state.removeFx(rPfx, fxIdx)
					return
				}
				if strings.Contains(name, p.fxPlugName) {
					pt, ok := p.fxPlugParams[name]
					if !ok {
						glog.Infof("Unknown plug %q", name)
						return
					}
					glog.Infof("Using %q at %d for FX on %s", name, fxIdx, mapping.reaperPrefix)
					//TODO handle different plugins for each FX type
					mapping.fxMap.eq = &fxInstance{vstIndex: fxIdx, params: &pt}
					mapping.fxMap.gate = &fxInstance{vstIndex: fxIdx, params: &pt}
					mapping.fxMap.dyn = &fxInstance{vstIndex: fxIdx, params: &pt}
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
							//glog.Errorf("%s: failed to handle message: %v", reaFxAddr, err)
							return
						}
						p.sendMessagesToX32(msgs)
					})
					if err != nil {
						glog.Errorf("%s: failed to add handler: %v", reaFxAddr, err)
					}
				} // paramIdx
			} // fxIdx
		}
	}

	/*
		for fxIdx := 0; fxIdx < 10; fxIdx++ {
			for paramIdx := 0; paramIdx < 300; paramIdx++ {
				if err := d.AddMsgHandler(fmt.Sprintf("/fx/%d/fxparam/%d/name", fxIdx, paramIdx), p.nameHandler); err != nil {
					return err
				}
			}
		}
	*/
	return nil
}

// configureX32Dispatcher configures a dispatcher with handlers for all X32
// originated OSC messages that we're interested in.
func (p *Proxy) configureX32Dispatcher(d osc.Dispatcher) error {
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
			if err := d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
				glog.V(2).Infof("X-> %s %v", x32Addr, msg.Arguments)
				msgs, err := tt.Apply(mapping, *msg)
				if err != nil {
					glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
					return
				}
				p.sendMessagesToReaper(msgs)
			}); err != nil {
				return err
			}
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
			if err := d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
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
			}); err != nil {
				return err
			}
		}
	}

	// Fan-out stat
	for xStat, tt := range x32ReaperFanoutStatMap {
		xStat := xStat
		tt := tt
		tt.state = &p._state
		tt.nameHints = p.nameHints

		x32Addr := fmt.Sprintf("/%s", xStat)
		if err := d.AddMsgHandler(x32Addr, func(msg *osc.Message) {
			glog.V(2).Infof("X-> %s %v", x32Addr, msg.Arguments)
			msgs, err := tt.Apply(&mapping{}, *msg)
			if err != nil {
				glog.Errorf("%s: failed to handle message: %v", x32Addr, err)
				return
			}
			p.sendMessagesToReaper(msgs)
		}); err != nil {
			return err
		}
	}

	return nil
}
