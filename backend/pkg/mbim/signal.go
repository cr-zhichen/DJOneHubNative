package mbim

import (
	"context"
	"fmt"
)

// SignalState is the parsed Signal State response.
type SignalState struct {
	RSSI      uint32
	DBM       int
	Unknown   bool
	ErrorRate uint32

	// RSRP/SNR are only present when the device emits the MBIMEx 2.0
	// MBIM_SIGNAL_STATE_INFO_V2 payload (requires MBIMEx version negotiation).
	// MBIMEx signal state carries RSRP and SNR only — there is no RSRQ field.
	RSRP    int // dBm
	HasRSRP bool
	SNR     int // dB (maps to the panel's SINR)
	HasSNR  bool
}

const (
	signalFixedLen   = 5 * 4
	signalV2FixedLen = 7 * 4 // adds RsrpSnrOffset + RsrpSnrSize
	rsrpSnrInfoLen   = 5 * 4

	dataClassLTE   = 0x20
	dataClass5GNSA = 0x40
	dataClass5GSA  = 0x80
)

func rssiToDBM(rssi uint32) (dbm int, unknown bool) {
	if rssi == 99 || rssi > 31 {
		return 0, true
	}
	return -113 + 2*int(rssi), false
}

// rsrpCodedToDBM maps the MBIM_RSRP_SNR_INFO coded RSRP (0..126, 127=unknown)
// to dBm: dBm = coded - 157.
func rsrpCodedToDBM(coded uint32) (int, bool) {
	if coded > 126 {
		return 0, false
	}
	return int(coded) - 157, true
}

// snrCodedToDB maps the coded SNR (0..127, 128=unknown) to dB:
// dB = coded*0.5 - 23 == (coded - 46) / 2.
func snrCodedToDB(coded uint32) (int, bool) {
	if coded > 127 {
		return 0, false
	}
	return (int(coded) - 46) / 2, true
}

func parseSignalState(info []byte) (SignalState, error) {
	if len(info) < signalFixedLen {
		return SignalState{}, fmt.Errorf("mbim: signal info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var s SignalState
	s.RSSI, _ = r.u32At(0)
	s.ErrorRate, _ = r.u32At(4)
	s.DBM, s.Unknown = rssiToDBM(s.RSSI)
	parseSignalRsrpSnrV2(info, &s)
	return s, nil
}

// parseSignalRsrpSnrV2 extracts RSRP/SNR from an MBIM_SIGNAL_STATE_INFO_V2
// payload when present. A V1 buffer is shorter than the V2 fixed head or
// carries a zero RsrpSnrOffset/Size, in which case nothing is filled.
func parseSignalRsrpSnrV2(info []byte, s *SignalState) {
	if len(info) < signalV2FixedLen {
		return
	}
	r := newInfoReader(info)
	// RsrpSnrOffset @20, RsrpSnrSize @24 form an OFFSET/SIZE pair.
	buf, err := r.byteArrayAt(20)
	if err != nil || len(buf) < 4 {
		return
	}
	br := newInfoReader(buf)
	count, _ := br.u32At(0)
	for i := uint32(0); i < count; i++ {
		base := 4 + int(i)*rsrpSnrInfoLen
		if base+rsrpSnrInfoLen > len(buf) {
			break
		}
		rsrpCoded, _ := br.u32At(base)
		snrCoded, _ := br.u32At(base + 4)
		systemType, _ := br.u32At(base + 16)
		if systemType&(dataClassLTE|dataClass5GNSA|dataClass5GSA) == 0 {
			continue // RSRP/SNR only meaningful for LTE/5G
		}
		if v, ok := rsrpCodedToDBM(rsrpCoded); ok {
			s.RSRP, s.HasRSRP = v, true
		}
		if v, ok := snrCodedToDB(snrCoded); ok {
			s.SNR, s.HasSNR = v, true
		}
		if s.HasRSRP || s.HasSNR {
			return
		}
	}
}

// QuerySignalState issues SIGNAL_STATE and parses the response.
func QuerySignalState(ctx context.Context, d *Device) (SignalState, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectSignalState, CommandTypeQuery, nil)
	if err != nil {
		return SignalState{}, err
	}
	if resp.Status != 0 {
		return SignalState{}, fmt.Errorf("mbim: SIGNAL_STATE status=%d", resp.Status)
	}
	return parseSignalState(resp.InfoBuffer)
}
