package mbim

import (
	"sync"
	"time"
)

// Snapshot is a point-in-time view of modem state, updated from indications.
type Snapshot struct {
	ReadyState    uint32
	IMSI          string
	ICCID         string
	RegisterState uint32
	ProviderName  string
	MCC           string
	MNC           string
	PacketState   uint32
	SignalRSSI    uint32
	SignalDBM     int
	SignalUnknown bool
	UpdatedAt     time.Time
}

// Monitor maintains a Snapshot from a Device's indication stream.
type Monitor struct {
	dev               *Device
	mu                sync.RWMutex
	snap              Snapshot
	done              chan struct{}
	once              sync.Once
	onSMS             func()
	onConnect         func(ConnectState)
	onUSSD            func(USSDResponse)
	onSubscriberReady func(Snapshot)
	onSlotInfoStatus  func(SlotInfoStatus)
}

// NewMonitor creates a Monitor bound to a Device.
func NewMonitor(d *Device) *Monitor {
	return &Monitor{dev: d, done: make(chan struct{})}
}

// Run consumes indications until Stop is called or the Device's channel closes.
func (m *Monitor) Run() {
	for {
		select {
		case <-m.done:
			return
		case ind, ok := <-m.dev.Indications():
			if !ok {
				return
			}
			m.apply(ind)
		}
	}
}

// Stop ends the Run loop. It is safe to call multiple times.
func (m *Monitor) Stop() {
	m.once.Do(func() { close(m.done) })
}

// Snapshot returns a copy of the current state.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap
}

// SetOnSMS registers a callback fired on every SMS_READ indication.
func (m *Monitor) SetOnSMS(cb func()) {
	m.mu.Lock()
	m.onSMS = cb
	m.mu.Unlock()
}

func (m *Monitor) SetOnConnect(cb func(ConnectState)) {
	m.mu.Lock()
	m.onConnect = cb
	m.mu.Unlock()
}

// SetOnUSSD registers a callback fired on every parsed USSD indication.
func (m *Monitor) SetOnUSSD(cb func(USSDResponse)) {
	m.mu.Lock()
	m.onUSSD = cb
	m.mu.Unlock()
}

// SetOnSubscriberReady registers a callback fired when the parsed
// SUBSCRIBER_READY_STATUS indication changes ReadyState or ICCID relative to
// the previous snapshot (SIM inserted/removed/swapped).
func (m *Monitor) SetOnSubscriberReady(cb func(Snapshot)) {
	m.mu.Lock()
	m.onSubscriberReady = cb
	m.mu.Unlock()
}

// SetOnSlotInfoStatus registers a callback fired on every SLOT_INFO_STATUS indication.
func (m *Monitor) SetOnSlotInfoStatus(cb func(SlotInfoStatus)) {
	m.mu.Lock()
	m.onSlotInfoStatus = cb
	m.mu.Unlock()
}

func (m *Monitor) apply(ind Indication) {
	switch {
	case ind.Service.Equal(UUIDSMS):
		if ind.CID == CIDSMSRead {
			m.mu.RLock()
			cb := m.onSMS
			m.mu.RUnlock()
			if cb != nil {
				cb()
			}
		}
		return
	case ind.Service.Equal(UUIDUSSD):
		if ind.CID == CIDUSSD {
			resp, err := parseUSSDResponse(ind.InfoBuffer)
			if err != nil {
				return
			}
			m.mu.RLock()
			cb := m.onUSSD
			m.mu.RUnlock()
			if cb != nil {
				cb(resp)
			}
		}
		return
	case ind.Service.Equal(UUIDMSBasicConnectExtensions):
		if ind.CID == CIDMSBasicConnectExtSlotInfoStatus {
			if s, err := parseSlotInfoStatus(ind.InfoBuffer); err == nil {
				m.mu.RLock()
				cb := m.onSlotInfoStatus
				m.mu.RUnlock()
				if cb != nil {
					cb(s)
				}
			}
		}
		return
	case ind.Service.Equal(UUIDBasicConnect):
	default:
		return
	}

	if ind.CID == CIDBasicConnectConnect {
		if st, err := parseConnect(ind.InfoBuffer); err == nil {
			m.mu.RLock()
			cb := m.onConnect
			m.mu.RUnlock()
			if cb != nil {
				cb(st)
			}
		}
		return
	}

	m.mu.Lock()
	var fireSubscriberReady bool
	var firedSnap Snapshot
	switch ind.CID {
	case CIDBasicConnectSignalState:
		if s, err := parseSignalState(ind.InfoBuffer); err == nil {
			m.snap.SignalRSSI = s.RSSI
			m.snap.SignalDBM = s.DBM
			m.snap.SignalUnknown = s.Unknown
		}
	case CIDBasicConnectRegisterState:
		if rs, err := parseRegisterState(ind.InfoBuffer); err == nil {
			m.snap.RegisterState = rs.RegisterState
			m.snap.ProviderName = rs.ProviderName
			m.snap.MCC = rs.MCC
			m.snap.MNC = rs.MNC
		}
	case CIDBasicConnectPacketService:
		if ps, err := parsePacketService(ind.InfoBuffer); err == nil {
			m.snap.PacketState = ps.State
		}
	case CIDBasicConnectSubscriberReadyStatus:
		if sub, err := parseSubscriberReady(ind.InfoBuffer); err == nil {
			if sub.ReadyState != m.snap.ReadyState || sub.ICCID != m.snap.ICCID {
				fireSubscriberReady = true
			}
			m.snap.ReadyState = sub.ReadyState
			m.snap.IMSI = sub.IMSI
			m.snap.ICCID = sub.ICCID
		}
	default:
		m.mu.Unlock()
		return
	}
	m.snap.UpdatedAt = time.Now()
	if fireSubscriberReady {
		firedSnap = m.snap
	}
	cb := m.onSubscriberReady
	m.mu.Unlock()
	if fireSubscriberReady && cb != nil {
		cb(firedSnap)
	}
}
