package mbim

import (
	"context"
	"fmt"
	"sync"
)

// Device is an opened MBIM control endpoint with transaction multiplexing.
type Device struct {
	tr Transport

	mu        sync.Mutex
	writeMu   sync.Mutex
	nextTx    uint32
	pending   map[uint32]chan commandResult
	maxCtrl   uint32
	closed    bool
	collector map[uint32]*collector

	indications chan Indication
}

type commandResult struct {
	resp CommandDone
	err  error
}

// Indication is an unsolicited INDICATE_STATUS message.
type Indication struct {
	Service    UUID
	CID        uint32
	InfoBuffer []byte
}

// NewDevice constructs an MBIM device client around an established transport.
func NewDevice(tr Transport) *Device {
	return newDevice(tr)
}

func newDevice(tr Transport) *Device {
	return &Device{
		tr:          tr,
		pending:     make(map[uint32]chan commandResult),
		collector:   make(map[uint32]*collector),
		indications: make(chan Indication, 16),
	}
}

// Open performs the MBIM OPEN handshake and starts the read loop.
func (d *Device) Open(ctx context.Context, maxControlTransfer uint32) error {
	d.mu.Lock()
	d.maxCtrl = maxControlTransfer
	d.mu.Unlock()
	go d.readLoop()

	// 走 mbim-proxy 时必须先发 PROXY_CONFIG 告诉代理要打开哪个底层设备,
	// 之后 OPEN 才能被代理正确转发——顺序与 libmbim / `mbimcli -p` 一致。
	// 顺序反了(先 OPEN)代理不知道目标设备,DeviceCaps 等后续命令会失败。
	if pc, ok := d.tr.(proxyConfigurer); ok {
		if path, need := pc.needsProxyConfig(); need {
			info := encodeProxyConfigInfo(path, 30)
			if _, err := d.Command(ctx, UUIDProxyControl, CIDProxyControlConfiguration, CommandTypeSet, info); err != nil {
				return fmt.Errorf("mbim: proxy configuration: %w", err)
			}
		}
	}

	tx := d.allocTx()
	ch := d.registerPending(tx)
	if err := d.tr.WriteMessage(encodeOpen(tx, maxControlTransfer)); err != nil {
		d.removePending(tx)
		return fmt.Errorf("mbim: send OPEN: %w", err)
	}

	select {
	case <-ctx.Done():
		d.removePending(tx)
		return ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return result.err
		}
		if result.resp.Status != 0 {
			return fmt.Errorf("mbim: OPEN_DONE status=%d", result.resp.Status)
		}
	}
	return nil
}

func (d *Device) allocTx() uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextTx++
	return d.nextTx
}

func (d *Device) registerPending(tx uint32) chan commandResult {
	ch := make(chan commandResult, 1)
	d.mu.Lock()
	d.pending[tx] = ch
	d.mu.Unlock()
	return ch
}

// Command sends a COMMAND and waits for the matching COMMAND_DONE response.
func (d *Device) Command(ctx context.Context, service UUID, cid uint32, ct CommandType, info []byte) (CommandDone, error) {
	tx := d.allocTx()
	ch := d.registerPending(tx)

	d.mu.Lock()
	maxCtrl := d.maxCtrl
	d.mu.Unlock()
	frags := splitCommand(tx, service, cid, ct, info, maxCtrl)
	d.writeMu.Lock()
	for _, f := range frags {
		if err := d.tr.WriteMessage(f); err != nil {
			d.writeMu.Unlock()
			d.removePending(tx)
			return CommandDone{}, fmt.Errorf("mbim: send COMMAND: %w", err)
		}
	}
	d.writeMu.Unlock()

	select {
	case <-ctx.Done():
		d.removePending(tx)
		return CommandDone{}, ctx.Err()
	case result := <-ch:
		if result.err != nil {
			return CommandDone{}, result.err
		}
		return result.resp, nil
	}
}

// Indications returns unsolicited INDICATE_STATUS messages.
func (d *Device) Indications() <-chan Indication {
	return d.indications
}

func (d *Device) readLoop() {
	for {
		msg, err := d.tr.ReadMessage()
		if err != nil {
			d.failPending(fmt.Errorf("mbim: read message: %w", err))
			return
		}
		h, err := decodeHeader(msg)
		if err != nil {
			continue
		}

		switch h.Type {
		case MessageTypeOpenDone, MessageTypeCloseDone:
			if len(msg) < headerLen+4 {
				continue
			}
			d.deliver(h.TransactionID, CommandDone{Status: le.Uint32(msg[headerLen:])})
		case MessageTypeCommandDone:
			d.handleCommandDoneFragment(h.TransactionID, msg)
		case MessageTypeIndicateStatus:
			d.handleIndicationFragment(h.TransactionID, msg)
		}
	}
}

func (d *Device) handleCommandDoneFragment(tx uint32, msg []byte) {
	c := d.commandCollector(tx)
	done, err := c.add(msg)
	if err != nil || !done {
		return
	}

	resp, err := c.commandDone()
	d.removeCollector(tx)
	if err != nil {
		d.deliverError(tx, err)
		return
	}
	d.deliver(tx, resp)
}

func (d *Device) handleIndicationFragment(tx uint32, msg []byte) {
	c := d.commandCollector(tx)
	done, err := c.add(commandDoneShapeIndication(msg))
	if err != nil || !done {
		return
	}

	resp, err := c.commandDone()
	d.removeCollector(tx)
	if err != nil {
		return
	}
	select {
	case d.indications <- Indication{Service: resp.Service, CID: resp.CID, InfoBuffer: resp.InfoBuffer}:
	default:
	}
}

func (d *Device) commandCollector(tx uint32) *collector {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.collector[tx]
	if c == nil {
		c = newCollector()
		d.collector[tx] = c
	}
	return c
}

func (d *Device) removeCollector(tx uint32) {
	d.mu.Lock()
	delete(d.collector, tx)
	d.mu.Unlock()
}

func (d *Device) removePending(tx uint32) {
	d.mu.Lock()
	delete(d.pending, tx)
	d.mu.Unlock()
}

func (d *Device) deliver(tx uint32, resp CommandDone) {
	d.mu.Lock()
	ch := d.pending[tx]
	delete(d.pending, tx)
	d.mu.Unlock()
	if ch != nil {
		ch <- commandResult{resp: resp}
	}
}

func (d *Device) deliverError(tx uint32, err error) {
	d.mu.Lock()
	ch := d.pending[tx]
	delete(d.pending, tx)
	d.mu.Unlock()
	if ch != nil {
		ch <- commandResult{err: err}
	}
}

func (d *Device) failPending(err error) {
	d.mu.Lock()
	pending := d.pending
	d.pending = make(map[uint32]chan commandResult)
	d.collector = make(map[uint32]*collector)
	d.mu.Unlock()
	for _, ch := range pending {
		ch <- commandResult{err: err}
	}
}

// commandDoneShapeIndication inserts a zero status field so the shared
// collector can reassemble INDICATE_STATUS fragments.
func commandDoneShapeIndication(msg []byte) []byte {
	if len(msg) < headerLen+fragHdrLen {
		return msg
	}
	current := le.Uint32(msg[headerLen+4:])
	if current != 0 {
		return msg
	}
	if len(msg) < headerLen+fragHdrLen+uuidLen+4+4 {
		return msg
	}

	out := make([]byte, len(msg)+4)
	copy(out[:headerLen+fragHdrLen+uuidLen+4], msg[:headerLen+fragHdrLen+uuidLen+4])
	le.PutUint32(out[4:], uint32(len(out)))
	copy(out[headerLen+fragHdrLen+uuidLen+4+4:], msg[headerLen+fragHdrLen+uuidLen+4:])
	return out
}

// Close sends MBIM CLOSE and closes the transport. It is idempotent.
func (d *Device) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.nextTx++
	tx := d.nextTx
	d.mu.Unlock()

	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_ = d.tr.WriteMessage(encodeClose(tx))
	return d.tr.Close()
}
