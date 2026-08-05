package mbim

import (
	"context"
	"fmt"
)

// PacketServiceAction values for the PACKET_SERVICE SET request.
type PacketServiceAction uint32

const (
	PacketServiceDetach PacketServiceAction = 0
	PacketServiceAttach PacketServiceAction = 1
)

// PacketService is the parsed Packet Service response.
type PacketService struct {
	NwError       uint32
	State         uint32
	HighestClass  uint32
	UplinkSpeed   uint64
	DownlinkSpeed uint64
}

const packetFixedLen = 3*4 + 2*8

func parsePacketService(info []byte) (PacketService, error) {
	if len(info) < packetFixedLen {
		return PacketService{}, fmt.Errorf("mbim: packet info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var ps PacketService
	ps.NwError, _ = r.u32At(0)
	ps.State, _ = r.u32At(4)
	ps.HighestClass, _ = r.u32At(8)
	ps.UplinkSpeed, _ = r.u64At(12)
	ps.DownlinkSpeed, _ = r.u64At(20)
	return ps, nil
}

// QueryPacketService issues PACKET_SERVICE query and parses the response.
func QueryPacketService(ctx context.Context, d *Device) (PacketService, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectPacketService, CommandTypeQuery, nil)
	if err != nil {
		return PacketService{}, err
	}
	if resp.Status != 0 {
		return PacketService{}, fmt.Errorf("mbim: PACKET_SERVICE status=%d", resp.Status)
	}
	return parsePacketService(resp.InfoBuffer)
}

// SetPacketService attaches or detaches the packet service.
func SetPacketService(ctx context.Context, d *Device, action PacketServiceAction) (PacketService, error) {
	info := make([]byte, 4)
	le.PutUint32(info, uint32(action))
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectPacketService, CommandTypeSet, info)
	if err != nil {
		return PacketService{}, err
	}
	if resp.Status != 0 {
		return PacketService{}, fmt.Errorf("mbim: PACKET_SERVICE set status=%d", resp.Status)
	}
	return parsePacketService(resp.InfoBuffer)
}
