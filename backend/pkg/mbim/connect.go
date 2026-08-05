package mbim

import (
	"context"
	"fmt"
)

const (
	ActivationCommandDeactivate uint32 = 0
	ActivationCommandActivate   uint32 = 1
)

const (
	ActivationStateUnknown      uint32 = 0
	ActivationStateActivated    uint32 = 1
	ActivationStateActivating   uint32 = 2
	ActivationStateDeactivated  uint32 = 3
	ActivationStateDeactivating uint32 = 4
)

const (
	ContextIPTypeDefault uint32 = 0
	ContextIPTypeIPv4    uint32 = 1
	ContextIPTypeIPv6    uint32 = 2
	ContextIPTypeIPv4v6  uint32 = 3
)

const (
	AuthProtocolNone uint32 = 0
	AuthProtocolPAP  uint32 = 1
	AuthProtocolCHAP uint32 = 2
)

type ConnectState struct {
	SessionID       uint32
	ActivationState uint32
	IPType          uint32
	NwError         uint32
}

func encodeConnect(sessionID, command uint32, accessString, userName, password string, authProtocol, ipType uint32) []byte {
	const fixed = 60
	access := encodeUTF16LE(accessString)
	user := encodeUTF16LE(userName)
	pass := encodeUTF16LE(password)
	accessPad, userPad, passPad := pad4(len(access)), pad4(len(user)), pad4(len(pass))

	info := make([]byte, fixed+accessPad+userPad+passPad)
	le.PutUint32(info[0:], sessionID)
	le.PutUint32(info[4:], command)

	off := fixed
	putRef := func(pos int, data []byte, padded int) {
		if len(data) == 0 {
			return
		}
		le.PutUint32(info[pos:], uint32(off))
		le.PutUint32(info[pos+4:], uint32(len(data)))
		copy(info[off:], data)
		off += padded
	}
	putRef(8, access, accessPad)
	putRef(16, user, userPad)
	putRef(24, pass, passPad)

	le.PutUint32(info[32:], 0)
	le.PutUint32(info[36:], authProtocol)
	le.PutUint32(info[40:], ipType)
	copy(info[44:60], UUIDContextTypeInternet[:])
	return info
}

func parseConnect(info []byte) (ConnectState, error) {
	const fixed = 36
	if len(info) < fixed {
		return ConnectState{}, fmt.Errorf("mbim: CONNECT response too short len=%d", len(info))
	}
	r := newInfoReader(info)
	sid, _ := r.u32At(0)
	state, _ := r.u32At(4)
	ipType, _ := r.u32At(12)
	nwErr, _ := r.u32At(32)
	return ConnectState{
		SessionID:       sid,
		ActivationState: state,
		IPType:          ipType,
		NwError:         nwErr,
	}, nil
}

func Connect(ctx context.Context, d *Device, sessionID, command uint32, accessString, userName, password string, authProtocol, ipType uint32) (ConnectState, error) {
	info := encodeConnect(sessionID, command, accessString, userName, password, authProtocol, ipType)
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectConnect, CommandTypeSet, info)
	if err != nil {
		return ConnectState{}, err
	}
	if resp.Status != 0 {
		return ConnectState{}, &StatusError{Op: "CONNECT", Status: resp.Status}
	}
	return parseConnect(resp.InfoBuffer)
}

func ParseConnectIndication(info []byte) (ConnectState, error) {
	return parseConnect(info)
}
