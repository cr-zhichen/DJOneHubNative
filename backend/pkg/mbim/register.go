package mbim

import (
	"context"
	"fmt"
	"unicode/utf16"
)

// RegisterState is the parsed Register State response.
type RegisterState struct {
	NwError       uint32
	RegisterState uint32
	RegisterMode  uint32
	ProviderID    string
	ProviderName  string
	MCC           string
	MNC           string
}

const registerFixedLen = 5*4 + 3*8

func parseRegisterState(info []byte) (RegisterState, error) {
	if len(info) < registerFixedLen {
		return RegisterState{}, fmt.Errorf("mbim: register info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var rs RegisterState
	rs.NwError, _ = r.u32At(0)
	rs.RegisterState, _ = r.u32At(4)
	rs.RegisterMode, _ = r.u32At(8)
	var err error
	if rs.ProviderID, err = r.stringAt(20); err != nil {
		return RegisterState{}, fmt.Errorf("mbim: parse ProviderId: %w", err)
	}
	if rs.ProviderName, err = r.stringAt(28); err != nil {
		return RegisterState{}, fmt.Errorf("mbim: parse ProviderName: %w", err)
	}
	rs.MCC, rs.MNC = splitPLMN(rs.ProviderID)
	return rs, nil
}

func splitPLMN(plmn string) (mcc, mnc string) {
	if len(plmn) < 5 {
		return "", ""
	}
	return plmn[:3], plmn[3:]
}

// QueryRegisterState issues REGISTER_STATE and parses the response.
func QueryRegisterState(ctx context.Context, d *Device) (RegisterState, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectRegisterState, CommandTypeQuery, nil)
	if err != nil {
		return RegisterState{}, err
	}
	if resp.Status != 0 {
		return RegisterState{}, fmt.Errorf("mbim: REGISTER_STATE status=%d", resp.Status)
	}
	return parseRegisterState(resp.InfoBuffer)
}

func encodeSetRegisterState(action uint32, plmn string) []byte {
	const fixed = 16
	var providerID []byte
	if plmn != "" {
		providerID = encodeUTF16LE(plmn)
	}
	info := make([]byte, fixed+len(providerID))
	if len(providerID) > 0 {
		le.PutUint32(info[0:], fixed)
		le.PutUint32(info[4:], uint32(len(providerID)))
		copy(info[fixed:], providerID)
	}
	le.PutUint32(info[8:], action)
	return info
}

// SetRegisterState issues REGISTER_STATE set and parses the modem response.
func SetRegisterState(ctx context.Context, d *Device, action uint32, plmn string) (RegisterState, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectRegisterState, CommandTypeSet, encodeSetRegisterState(action, plmn))
	if err != nil {
		return RegisterState{}, err
	}
	if resp.Status != 0 {
		return RegisterState{}, fmt.Errorf("mbim: REGISTER_STATE set status=%d", resp.Status)
	}
	return parseRegisterState(resp.InfoBuffer)
}

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	b := make([]byte, len(units)*2)
	for i, c := range units {
		le.PutUint16(b[i*2:], c)
	}
	return b
}
