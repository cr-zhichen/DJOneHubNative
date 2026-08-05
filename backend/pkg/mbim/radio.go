package mbim

import (
	"context"
	"fmt"
)

// RadioSwitch is the radio on/off state.
type RadioSwitch uint32

const (
	RadioOff RadioSwitch = 0
	RadioOn  RadioSwitch = 1
)

// RadioState is the parsed Radio State response.
type RadioState struct {
	Hardware RadioSwitch
	Software RadioSwitch
}

func parseRadioState(info []byte) (RadioState, error) {
	if len(info) < 8 {
		return RadioState{}, fmt.Errorf("mbim: radio info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	hw, _ := r.u32At(0)
	sw, _ := r.u32At(4)
	return RadioState{Hardware: RadioSwitch(hw), Software: RadioSwitch(sw)}, nil
}

// QueryRadioState issues RADIO_STATE query.
func QueryRadioState(ctx context.Context, d *Device) (RadioState, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectRadioState, CommandTypeQuery, nil)
	if err != nil {
		return RadioState{}, err
	}
	if resp.Status != 0 {
		return RadioState{}, fmt.Errorf("mbim: RADIO_STATE status=%d", resp.Status)
	}
	return parseRadioState(resp.InfoBuffer)
}

// SetRadioState turns the software radio on or off.
func SetRadioState(ctx context.Context, d *Device, sw RadioSwitch) (RadioState, error) {
	info := make([]byte, 4)
	le.PutUint32(info, uint32(sw))
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectRadioState, CommandTypeSet, info)
	if err != nil {
		return RadioState{}, err
	}
	if resp.Status != 0 {
		return RadioState{}, fmt.Errorf("mbim: RADIO_STATE set status=%d", resp.Status)
	}
	return parseRadioState(resp.InfoBuffer)
}
