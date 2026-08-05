package mbim

import (
	"context"
	"fmt"
)

// fixed16 returns a 16-byte copy of b (zero-padded / truncated).
func fixed16(b []byte) []byte {
	out := make([]byte, 16)
	copy(out, b)
	return out
}

// encodeAuthAKA builds the AUTH_AKA query info buffer: Rand[16] || Autn[16]
// (fixed-size inline byte arrays).
func encodeAuthAKA(rand, autn []byte) []byte {
	info := make([]byte, 32)
	copy(info[0:16], fixed16(rand))
	copy(info[16:32], fixed16(autn))
	return info
}

// mbimStatusAuthSyncFailure は MBIM_STATUS_AUTH_SYNC_FAILURE (35):
// SQN out-of-range; the modem still populates the InfoBuffer with AUTS[14] at bytes 52-65.
const mbimStatusAuthSyncFailure = 35

// AuthAKA runs MBIM Auth AKA: given RAND/AUTN (16 bytes each), returns RES
// (truncated to ResLen), IK[16], CK[16] and AUTS[14]. AUTS is meaningful only
// on synchronization failure.
//
// On MBIM_STATUS_AUTH_SYNC_FAILURE (35) the InfoBuffer may still carry a valid
// response frame; AUTS is extracted and returned alongside the StatusError so
// callers can perform EAP-AKA resynchronization.
func AuthAKA(ctx context.Context, d *Device, rand, autn []byte) (res, ik, ck, auts []byte, err error) {
	resp, err := d.Command(ctx, UUIDAuth, CIDAuthAKA, CommandTypeQuery, encodeAuthAKA(rand, autn))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if resp.Status != 0 {
		se := &StatusError{Op: "AUTH_AKA", Status: resp.Status}
		// AUTH_SYNC_FAILURE: SQN mismatch. Extract AUTS from the response body so
		// the caller can build an EAP-AKA Synchronization-Failure message.
		if resp.Status == mbimStatusAuthSyncFailure {
			b := resp.InfoBuffer
			if len(b) >= 66 {
				auts = append([]byte(nil), b[52:66]...)
			} else if len(b) >= 14 {
				auts = append([]byte(nil), b[:14]...)
			}
		}
		return nil, nil, nil, auts, se
	}
	b := resp.InfoBuffer
	// Res[16] + ResLen(u32) + IK[16] + CK[16] + Auts[14] = 66
	if len(b) < 66 {
		return nil, nil, nil, nil, fmt.Errorf("mbim: AUTH_AKA response too short len=%d", len(b))
	}
	resLen := le.Uint32(b[16:20])
	if resLen > 16 {
		resLen = 16
	}
	res = append([]byte(nil), b[0:resLen]...)
	ik = append([]byte(nil), b[20:36]...)
	ck = append([]byte(nil), b[36:52]...)
	auts = append([]byte(nil), b[52:66]...)
	return res, ik, ck, auts, nil
}

// encodeAuthSIM builds the AUTH_SIM(2G GSM) query: Rand1[16]||Rand2[16]||Rand3[16]||N.
func encodeAuthSIM(rand1, rand2, rand3 []byte, n uint32) []byte {
	info := make([]byte, 52)
	copy(info[0:16], fixed16(rand1))
	copy(info[16:32], fixed16(rand2))
	copy(info[32:48], fixed16(rand3))
	le.PutUint32(info[48:], n)
	return info
}

// AuthSIM runs MBIM Auth SIM (2G GSM A3/A8) for a single RAND and returns the
// resulting SRES and Kc. Unlike AKA, GSM authentication has no AUTN/MAC to
// verify, so a functional Auth subsystem returns SRES/Kc for any RAND — this is
// the discriminating probe for "Auth service is a stub" vs "Auth works".
func AuthSIM(ctx context.Context, d *Device, rand []byte) (sres uint32, kc uint64, err error) {
	resp, err := d.Command(ctx, UUIDAuth, CIDAuthSIM, CommandTypeQuery, encodeAuthSIM(rand, nil, nil, 1))
	if err != nil {
		return 0, 0, err
	}
	if resp.Status != 0 {
		return 0, 0, &StatusError{Op: "AUTH_SIM", Status: resp.Status}
	}
	b := resp.InfoBuffer
	// Sres1(u32) + Kc1(u64) + ... ;只取第一组(N=1)。
	if len(b) < 12 {
		return 0, 0, fmt.Errorf("mbim: AUTH_SIM response too short len=%d", len(b))
	}
	sres = le.Uint32(b[0:4])
	kc = le.Uint64(b[4:12])
	return sres, kc, nil
}
