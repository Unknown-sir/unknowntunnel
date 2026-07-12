package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Unknown-sir/Unknowntunnel/internal/config"
	"github.com/Unknown-sir/Unknowntunnel/internal/protocol"
	"github.com/Unknown-sir/Unknowntunnel/internal/transport"
	"github.com/Unknown-sir/Unknowntunnel/internal/tun"
)

const (
	tcpChunkSize        = 1200
	udpChunkSize        = 1200
	maxUDPDatagram      = 65507
	maxTCPReorder       = 4096
	udpFlowIdle         = 2 * time.Minute
	udpAssemblyLifetime = 30 * time.Second
	dedupeWindow        = 1 << 20
	maxTCPConnections   = 65536
	maxUDPFlows         = 65536
	maxUDPAssemblies    = 4096
)

type App struct {
	cfg       *config.Config
	manager   *transport.Manager
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	msgID     atomic.Uint64
	closed    atomic.Bool
	closeOnce sync.Once

	tunMu  sync.RWMutex
	tunDev *tun.Device

	closersMu sync.Mutex
	closers   []io.Closer

	tcpMu    sync.Mutex
	tcpConns map[uint64]*tcpState

	udpMu        sync.Mutex
	entryUDP     map[uint64]*entryUDPFlow
	entryUDPKeys map[string]uint64
	remoteUDP    map[uint64]*remoteUDPFlow
	assemblies   map[udpAssemblyKey]*udpAssembly

	seenMu   sync.Mutex
	seen     map[uint64]struct{}
	seenRing []uint64
	seenNext int
}

type tcpState struct {
	conn      net.Conn
	ready     chan error
	sendSeq   atomic.Uint64
	mu        sync.Mutex
	expected  uint64
	reorder   map[uint64][]byte
	closeOnce sync.Once
}

type entryUDPFlow struct {
	pc      net.PacketConn
	client  net.Addr
	service string
	last    time.Time
	seq     atomic.Uint64
}

type remoteUDPFlow struct {
	conn    *net.UDPConn
	service string
	last    time.Time
	seq     atomic.Uint64
}

type udpAssemblyKey struct {
	flowID     uint64
	datagramID uint64
}

type udpAssembly struct {
	created  time.Time
	name     string
	total    int
	received int
	chunks   [][]byte
}

func New(cfg *config.Config, secret []byte) *App {
	seed, _ := randomID()
	a := &App{
		cfg:          cfg,
		manager:      transport.NewManager(cfg, secret),
		tcpConns:     make(map[uint64]*tcpState),
		entryUDP:     make(map[uint64]*entryUDPFlow),
		entryUDPKeys: make(map[string]uint64),
		remoteUDP:    make(map[uint64]*remoteUDPFlow),
		assemblies:   make(map[udpAssemblyKey]*udpAssembly),
		seen:         make(map[uint64]struct{}, dedupeWindow),
		seenRing:     make([]uint64, dedupeWindow),
	}
	a.msgID.Store(seed)
	return a
}

func (a *App) Run(parent context.Context) error {
	a.ctx, a.cancel = context.WithCancel(parent)
	if err := a.manager.Start(a.ctx); err != nil {
		return err
	}
	if a.cfg.L3.Enabled {
		dev, err := tun.Open(a.cfg.L3)
		if err != nil {
			_ = a.manager.Close()
			return err
		}
		a.tunMu.Lock()
		a.tunDev = dev
		a.tunMu.Unlock()
		log.Printf("L3 TUN interface %s is up with %s (MTU %d)", dev.Name(), a.cfg.L3.Address, a.cfg.L3.MTU)
		a.wg.Add(1)
		go a.tunReadLoop(dev)
	}

	a.wg.Add(1)
	go a.incomingLoop()
	a.wg.Add(1)
	go a.maintenanceLoop()

	for _, forward := range a.cfg.Forwards {
		var err error
		if forward.Protocol == "tcp" {
			err = a.startTCPForward(forward)
		} else {
			err = a.startUDPForward(forward)
		}
		if err != nil {
			a.Close()
			return err
		}
	}

	<-a.ctx.Done()
	a.Close()
	return nil
}

func (a *App) Close() {
	a.closeOnce.Do(func() {
		a.closed.Store(true)
		if a.cancel != nil {
			a.cancel()
		}
		a.closersMu.Lock()
		for _, closer := range a.closers {
			_ = closer.Close()
		}
		a.closers = nil
		a.closersMu.Unlock()

		a.tunMu.Lock()
		if a.tunDev != nil {
			_ = a.tunDev.Close()
			a.tunDev = nil
		}
		a.tunMu.Unlock()

		a.tcpMu.Lock()
		for id, state := range a.tcpConns {
			state.close()
			delete(a.tcpConns, id)
		}
		a.tcpMu.Unlock()

		a.udpMu.Lock()
		for _, flow := range a.remoteUDP {
			_ = flow.conn.Close()
		}
		a.remoteUDP = make(map[uint64]*remoteUDPFlow)
		a.entryUDP = make(map[uint64]*entryUDPFlow)
		a.entryUDPKeys = make(map[string]uint64)
		a.assemblies = make(map[udpAssemblyKey]*udpAssembly)
		a.udpMu.Unlock()

		_ = a.manager.Close()
		a.wg.Wait()
	})
}

func (a *App) incomingLoop() {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case raw := <-a.manager.Incoming():
			msg, err := protocol.Decode(raw)
			if err != nil {
				log.Printf("dropped invalid tunnel message: %v", err)
				continue
			}
			if a.isDuplicate(msg.ID) {
				continue
			}
			a.handleMessage(msg)
		}
	}
}

func (a *App) handleMessage(msg protocol.Message) {
	switch msg.Type {
	case protocol.TypeIPPacket:
		a.handleIPPacket(msg.Payload)
	case protocol.TypeOpenTCP:
		go a.handleOpenTCP(msg)
	case protocol.TypeTCPStatus:
		a.handleTCPStatus(msg)
	case protocol.TypeTCPData:
		a.handleTCPData(msg)
	case protocol.TypeTCPClose:
		a.closeTCP(msg.ConnID, false)
	case protocol.TypeUDPData:
		a.handleUDPChunk(msg)
	case protocol.TypePing:
		_ = a.send(protocol.Message{Type: protocol.TypePong, Seq: msg.Seq})
	case protocol.TypePong:
		// Receipt of any authenticated frame is enough for liveness.
	}
}

func (a *App) send(msg protocol.Message) error {
	msg.ID = a.msgID.Add(1)
	encoded, err := protocol.Encode(msg)
	if err != nil {
		return err
	}
	return a.manager.Send(encoded)
}

func (a *App) tunReadLoop(dev *tun.Device) {
	defer a.wg.Done()
	buf := make([]byte, a.cfg.L3.MTU+128)
	for {
		n, err := dev.Read(buf)
		if err != nil {
			select {
			case <-a.ctx.Done():
				return
			default:
				log.Printf("TUN read stopped: %v", err)
				return
			}
		}
		packet := append([]byte(nil), buf[:n]...)
		if !tun.PacketAllowed(packet, a.cfg.L3.AllowProtocols) {
			continue
		}
		if err := a.send(protocol.Message{Type: protocol.TypeIPPacket, Payload: packet}); err != nil && !errors.Is(err, transport.ErrNoSession) {
			log.Printf("L3 packet send failed: %v", err)
		}
	}
}

func (a *App) handleIPPacket(packet []byte) {
	if !a.cfg.L3.Enabled || !tun.PacketAllowed(packet, a.cfg.L3.AllowProtocols) {
		return
	}
	a.tunMu.RLock()
	dev := a.tunDev
	a.tunMu.RUnlock()
	if dev == nil {
		return
	}
	n, err := dev.Write(packet)
	if err != nil || n != len(packet) {
		log.Printf("TUN write failed: wrote %d/%d bytes: %v", n, len(packet), err)
	}
}

func (a *App) startTCPForward(f config.Forward) error {
	listener, err := net.Listen("tcp", f.Listen)
	if err != nil {
		return fmt.Errorf("listen TCP forward %q on %s: %w", f.Name, f.Listen, err)
	}
	a.addCloser(listener)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-a.ctx.Done():
					return
				default:
					log.Printf("TCP forward %s accept error: %v", f.Name, err)
					continue
				}
			}
			go a.openLocalTCP(conn, f.Service)
		}
	}()
	log.Printf("L4 TCP forward %s listening on %s -> remote service %s", f.Name, f.Listen, f.Service)
	return nil
}

func (a *App) openLocalTCP(conn net.Conn, service string) {
	if a.closed.Load() {
		_ = conn.Close()
		return
	}
	connID, err := a.uniqueTCPID()
	if err != nil {
		_ = conn.Close()
		return
	}
	state := newTCPState(conn, true)
	a.tcpMu.Lock()
	a.tcpConns[connID] = state
	a.tcpMu.Unlock()
	if err := a.send(protocol.Message{Type: protocol.TypeOpenTCP, ConnID: connID, Name: service}); err != nil {
		a.closeTCP(connID, false)
		return
	}
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	select {
	case err := <-state.ready:
		if err != nil {
			log.Printf("remote TCP service %s rejected connection: %v", service, err)
			a.closeTCP(connID, false)
			return
		}
	case <-timer.C:
		log.Printf("remote TCP service %s open timed out", service)
		a.closeTCP(connID, true)
		return
	case <-a.ctx.Done():
		a.closeTCP(connID, false)
		return
	}
	go a.pumpTCP(connID, state)
}

func (a *App) handleOpenTCP(msg protocol.Message) {
	if a.closed.Load() {
		return
	}
	a.tcpMu.Lock()
	atCapacity := len(a.tcpConns) >= maxTCPConnections
	a.tcpMu.Unlock()
	if atCapacity {
		_ = a.send(protocol.Message{Type: protocol.TypeTCPStatus, ConnID: msg.ConnID, Error: "TCP connection limit reached"})
		return
	}
	svc, ok := a.cfg.Services[msg.Name]
	if !ok || svc.Network != "tcp" {
		_ = a.send(protocol.Message{Type: protocol.TypeTCPStatus, ConnID: msg.ConnID, Error: "service is not allowed or is not TCP"})
		return
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second}
	conn, err := dialer.DialContext(a.ctx, "tcp", svc.Address)
	if err != nil {
		_ = a.send(protocol.Message{Type: protocol.TypeTCPStatus, ConnID: msg.ConnID, Error: "remote service connection failed"})
		return
	}
	if a.closed.Load() {
		_ = conn.Close()
		return
	}
	state := newTCPState(conn, false)
	a.tcpMu.Lock()
	if _, exists := a.tcpConns[msg.ConnID]; exists {
		a.tcpMu.Unlock()
		_ = conn.Close()
		return
	}
	a.tcpConns[msg.ConnID] = state
	a.tcpMu.Unlock()
	if err := a.send(protocol.Message{Type: protocol.TypeTCPStatus, ConnID: msg.ConnID}); err != nil {
		a.closeTCP(msg.ConnID, false)
		return
	}
	go a.pumpTCP(msg.ConnID, state)
}

func (a *App) handleTCPStatus(msg protocol.Message) {
	a.tcpMu.Lock()
	state := a.tcpConns[msg.ConnID]
	a.tcpMu.Unlock()
	if state == nil || state.ready == nil {
		return
	}
	var err error
	if msg.Error != "" {
		err = errors.New(msg.Error)
	}
	select {
	case state.ready <- err:
	default:
	}
}

func (a *App) pumpTCP(connID uint64, state *tcpState) {
	buf := make([]byte, tcpChunkSize)
	for {
		n, err := state.conn.Read(buf)
		if n > 0 {
			seq := state.sendSeq.Add(1)
			if sendErr := a.send(protocol.Message{Type: protocol.TypeTCPData, ConnID: connID, Seq: seq, Payload: append([]byte(nil), buf[:n]...)}); sendErr != nil {
				err = sendErr
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("TCP flow %d closed: %v", connID, err)
			}
			break
		}
	}
	a.closeTCP(connID, true)
}

func (a *App) handleTCPData(msg protocol.Message) {
	a.tcpMu.Lock()
	state := a.tcpConns[msg.ConnID]
	a.tcpMu.Unlock()
	if state == nil {
		_ = a.send(protocol.Message{Type: protocol.TypeTCPClose, ConnID: msg.ConnID})
		return
	}
	if err := state.writeOrdered(msg.Seq, msg.Payload); err != nil {
		a.closeTCP(msg.ConnID, true)
	}
}

func (a *App) closeTCP(connID uint64, notify bool) {
	a.tcpMu.Lock()
	state := a.tcpConns[connID]
	if state != nil {
		delete(a.tcpConns, connID)
	}
	a.tcpMu.Unlock()
	if state != nil {
		state.close()
		if notify {
			_ = a.send(protocol.Message{Type: protocol.TypeTCPClose, ConnID: connID})
		}
	}
}

func newTCPState(conn net.Conn, pending bool) *tcpState {
	state := &tcpState{conn: conn, expected: 1, reorder: make(map[uint64][]byte)}
	if pending {
		state.ready = make(chan error, 1)
	}
	return state
}

func (s *tcpState) close() {
	s.closeOnce.Do(func() { _ = s.conn.Close() })
}

func (s *tcpState) writeOrdered(seq uint64, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq < s.expected {
		return nil
	}
	if seq-s.expected > maxTCPReorder {
		return errors.New("TCP flow sequence exceeded reorder window")
	}
	if seq > s.expected {
		if _, ok := s.reorder[seq]; !ok {
			s.reorder[seq] = append([]byte(nil), payload...)
		}
		return nil
	}
	if err := writeConnAll(s.conn, payload); err != nil {
		return err
	}
	s.expected++
	for {
		next, ok := s.reorder[s.expected]
		if !ok {
			break
		}
		delete(s.reorder, s.expected)
		if err := writeConnAll(s.conn, next); err != nil {
			return err
		}
		s.expected++
	}
	return nil
}

func (a *App) startUDPForward(f config.Forward) error {
	pc, err := net.ListenPacket("udp", f.Listen)
	if err != nil {
		return fmt.Errorf("listen UDP forward %q on %s: %w", f.Name, f.Listen, err)
	}
	a.addCloser(pc)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		buf := make([]byte, maxUDPDatagram)
		for {
			n, client, err := pc.ReadFrom(buf)
			if err != nil {
				select {
				case <-a.ctx.Done():
					return
				default:
					log.Printf("UDP forward %s read error: %v", f.Name, err)
					continue
				}
			}
			flowID, flow, err := a.getEntryUDPFlow(f.Name, f.Service, pc, client)
			if err != nil {
				continue
			}
			if err := a.sendUDPDatagram(flowID, f.Service, flow.seq.Add(1), buf[:n]); err != nil && !errors.Is(err, transport.ErrNoSession) {
				log.Printf("UDP forward %s send error: %v", f.Name, err)
			}
		}
	}()
	log.Printf("L4 UDP forward %s listening on %s -> remote service %s", f.Name, f.Listen, f.Service)
	return nil
}

func (a *App) getEntryUDPFlow(forwardName, service string, pc net.PacketConn, client net.Addr) (uint64, *entryUDPFlow, error) {
	if a.closed.Load() {
		return 0, nil, errors.New("application is closing")
	}
	key := forwardName + "|" + client.Network() + "|" + client.String()
	a.udpMu.Lock()
	defer a.udpMu.Unlock()
	if id, ok := a.entryUDPKeys[key]; ok {
		if flow := a.entryUDP[id]; flow != nil {
			flow.last = time.Now()
			return id, flow, nil
		}
	}
	if len(a.entryUDP) >= maxUDPFlows {
		return 0, nil, errors.New("UDP entry flow limit reached")
	}
	for attempts := 0; attempts < 10; attempts++ {
		id, err := randomID()
		if err != nil {
			return 0, nil, err
		}
		if _, used := a.entryUDP[id]; used {
			continue
		}
		flow := &entryUDPFlow{pc: pc, client: client, service: service, last: time.Now()}
		a.entryUDP[id] = flow
		a.entryUDPKeys[key] = id
		return id, flow, nil
	}
	return 0, nil, errors.New("could not allocate UDP flow ID")
}

func (a *App) sendUDPDatagram(flowID uint64, service string, datagramID uint64, data []byte) error {
	if len(data) > maxUDPDatagram {
		return errors.New("UDP datagram exceeds maximum size")
	}
	total := (len(data) + udpChunkSize - 1) / udpChunkSize
	if total == 0 {
		total = 1
	}
	for part := 0; part < total; part++ {
		start := part * udpChunkSize
		end := start + udpChunkSize
		if end > len(data) {
			end = len(data)
		}
		payload := make([]byte, 4+end-start)
		binary.BigEndian.PutUint16(payload[:2], uint16(part))
		binary.BigEndian.PutUint16(payload[2:4], uint16(total))
		copy(payload[4:], data[start:end])
		if err := a.send(protocol.Message{Type: protocol.TypeUDPData, ConnID: flowID, Seq: datagramID, Name: service, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) handleUDPChunk(msg protocol.Message) {
	if len(msg.Payload) < 4 {
		return
	}
	part := int(binary.BigEndian.Uint16(msg.Payload[:2]))
	total := int(binary.BigEndian.Uint16(msg.Payload[2:4]))
	if total < 1 || total > 128 || part < 0 || part >= total {
		return
	}
	key := udpAssemblyKey{flowID: msg.ConnID, datagramID: msg.Seq}
	a.udpMu.Lock()
	assembly := a.assemblies[key]
	if assembly == nil {
		if len(a.assemblies) >= maxUDPAssemblies {
			a.udpMu.Unlock()
			return
		}
		assembly = &udpAssembly{created: time.Now(), name: msg.Name, total: total, chunks: make([][]byte, total)}
		a.assemblies[key] = assembly
	}
	if assembly.total != total {
		delete(a.assemblies, key)
		a.udpMu.Unlock()
		return
	}
	if assembly.name == "" && msg.Name != "" {
		assembly.name = msg.Name
	}
	if assembly.chunks[part] == nil {
		assembly.chunks[part] = append([]byte(nil), msg.Payload[4:]...)
		assembly.received++
	}
	if assembly.received != assembly.total {
		a.udpMu.Unlock()
		return
	}
	name := assembly.name
	chunks := assembly.chunks
	delete(a.assemblies, key)
	a.udpMu.Unlock()

	totalLen := 0
	for _, chunk := range chunks {
		totalLen += len(chunk)
	}
	if totalLen > maxUDPDatagram {
		return
	}
	data := make([]byte, 0, totalLen)
	for _, chunk := range chunks {
		data = append(data, chunk...)
	}
	a.handleUDPDatagram(msg.ConnID, name, data)
}

func (a *App) handleUDPDatagram(flowID uint64, service string, data []byte) {
	if a.closed.Load() {
		return
	}
	a.udpMu.Lock()
	if entry := a.entryUDP[flowID]; entry != nil {
		entry.last = time.Now()
		pc, client := entry.pc, entry.client
		a.udpMu.Unlock()
		if _, err := pc.WriteTo(data, client); err != nil {
			log.Printf("UDP reply write failed: %v", err)
		}
		return
	}
	remote := a.remoteUDP[flowID]
	if remote == nil {
		if len(a.remoteUDP) >= maxUDPFlows {
			a.udpMu.Unlock()
			return
		}
		svc, ok := a.cfg.Services[service]
		if !ok || svc.Network != "udp" {
			a.udpMu.Unlock()
			return
		}
		addr, err := net.ResolveUDPAddr("udp", svc.Address)
		if err != nil {
			a.udpMu.Unlock()
			return
		}
		conn, err := net.DialUDP("udp", nil, addr)
		if err != nil {
			a.udpMu.Unlock()
			return
		}
		remote = &remoteUDPFlow{conn: conn, service: service, last: time.Now()}
		a.remoteUDP[flowID] = remote
		go a.remoteUDPReadLoop(flowID, remote)
	}
	remote.last = time.Now()
	conn := remote.conn
	a.udpMu.Unlock()
	if _, err := conn.Write(data); err != nil {
		log.Printf("remote UDP service write failed: %v", err)
		a.deleteRemoteUDP(flowID, remote)
	}
}

func (a *App) remoteUDPReadLoop(flowID uint64, flow *remoteUDPFlow) {
	buf := make([]byte, maxUDPDatagram)
	for {
		_ = flow.conn.SetReadDeadline(time.Now().Add(udpFlowIdle))
		n, err := flow.conn.Read(buf)
		if err != nil {
			a.deleteRemoteUDP(flowID, flow)
			return
		}
		a.udpMu.Lock()
		flow.last = time.Now()
		a.udpMu.Unlock()
		if err := a.sendUDPDatagram(flowID, "", flow.seq.Add(1), buf[:n]); err != nil {
			if !errors.Is(err, transport.ErrNoSession) {
				log.Printf("UDP reply tunnel send failed: %v", err)
			}
		}
	}
}

func (a *App) deleteRemoteUDP(flowID uint64, flow *remoteUDPFlow) {
	a.udpMu.Lock()
	if a.remoteUDP[flowID] == flow {
		delete(a.remoteUDP, flowID)
		_ = flow.conn.Close()
	}
	a.udpMu.Unlock()
}

func (a *App) maintenanceLoop() {
	defer a.wg.Done()
	heartbeat := time.NewTicker(10 * time.Second)
	cleanup := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	defer cleanup.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case now := <-heartbeat.C:
			_ = a.send(protocol.Message{Type: protocol.TypePing, Seq: uint64(now.UnixNano())})
		case now := <-cleanup.C:
			a.cleanup(now)
		}
	}
}

func (a *App) cleanup(now time.Time) {
	a.udpMu.Lock()
	for key, assembly := range a.assemblies {
		if now.Sub(assembly.created) > udpAssemblyLifetime {
			delete(a.assemblies, key)
		}
	}
	for id, flow := range a.entryUDP {
		if now.Sub(flow.last) > udpFlowIdle {
			delete(a.entryUDP, id)
			for key, keyID := range a.entryUDPKeys {
				if keyID == id {
					delete(a.entryUDPKeys, key)
				}
			}
		}
	}
	for id, flow := range a.remoteUDP {
		if now.Sub(flow.last) > udpFlowIdle {
			delete(a.remoteUDP, id)
			_ = flow.conn.Close()
		}
	}
	a.udpMu.Unlock()
}

func (a *App) isDuplicate(id uint64) bool {
	if id == 0 {
		return true
	}
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	if _, ok := a.seen[id]; ok {
		return true
	}
	old := a.seenRing[a.seenNext]
	if old != 0 {
		delete(a.seen, old)
	}
	a.seenRing[a.seenNext] = id
	a.seenNext++
	if a.seenNext == len(a.seenRing) {
		a.seenNext = 0
	}
	a.seen[id] = struct{}{}
	return false
}

func (a *App) uniqueTCPID() (uint64, error) {
	a.tcpMu.Lock()
	atCapacity := len(a.tcpConns) >= maxTCPConnections
	a.tcpMu.Unlock()
	if atCapacity {
		return 0, errors.New("TCP connection limit reached")
	}
	for attempts := 0; attempts < 10; attempts++ {
		id, err := randomID()
		if err != nil {
			return 0, err
		}
		a.tcpMu.Lock()
		_, exists := a.tcpConns[id]
		a.tcpMu.Unlock()
		if !exists {
			return id, nil
		}
	}
	return 0, errors.New("could not allocate TCP flow ID")
}

func randomID() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	id := binary.BigEndian.Uint64(b[:])
	if id == 0 {
		id = 1
	}
	return id, nil
}

func (a *App) addCloser(c io.Closer) {
	a.closersMu.Lock()
	a.closers = append(a.closers, c)
	a.closersMu.Unlock()
}

func writeConnAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
