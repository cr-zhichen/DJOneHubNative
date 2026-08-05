package mbim

import (
	"context"
	"fmt"
)

// EventEntry is one service plus the CIDs whose indications are requested.
type EventEntry struct {
	Service UUID
	CIDs    []uint32
}

func encodeSubscribeList(entries []EventEntry) []byte {
	n := len(entries)
	fixed := 4 + n*8
	type blob struct {
		off  int
		data []byte
	}
	blobs := make([]blob, 0, n)
	pos := fixed
	for _, e := range entries {
		data := make([]byte, 16+4+4*len(e.CIDs))
		copy(data[0:16], e.Service[:])
		le.PutUint32(data[16:], uint32(len(e.CIDs)))
		for i, cid := range e.CIDs {
			le.PutUint32(data[20+i*4:], cid)
		}
		blobs = append(blobs, blob{off: pos, data: data})
		pos += len(data)
	}

	info := make([]byte, pos)
	le.PutUint32(info[0:], uint32(n))
	for i, b := range blobs {
		le.PutUint32(info[4+i*8:], uint32(b.off))
		le.PutUint32(info[4+i*8+4:], uint32(len(b.data)))
		copy(info[b.off:], b.data)
	}
	return info
}

// Subscribe sends a DEVICE_SERVICE_SUBSCRIBE_LIST set request.
func Subscribe(ctx context.Context, d *Device, entries []EventEntry) error {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectDeviceServiceSubscribeList, CommandTypeSet, encodeSubscribeList(entries))
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return fmt.Errorf("mbim: SUBSCRIBE_LIST status=%d", resp.Status)
	}
	return nil
}

// SubscribeDefaultEvents subscribes to Basic Connect and SMS indications used by Monitor.
func SubscribeDefaultEvents(ctx context.Context, d *Device) error {
	return Subscribe(ctx, d, []EventEntry{
		{Service: UUIDBasicConnect, CIDs: []uint32{
			CIDBasicConnectSignalState,
			CIDBasicConnectRegisterState,
			CIDBasicConnectPacketService,
			CIDBasicConnectSubscriberReadyStatus,
		}},
		{Service: UUIDSMS, CIDs: []uint32{CIDSMSRead}},
	})
}
