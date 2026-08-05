package mbim

import (
	"context"
	"fmt"
)

func pad4(n int) int { return (n + 3) &^ 3 }

func encodeSMSSend(pdu []byte) []byte {
	const fixed = 12
	info := make([]byte, fixed+pad4(len(pdu)))
	le.PutUint32(info[0:], SMSFormatPDU)
	le.PutUint32(info[4:], fixed)
	le.PutUint32(info[8:], uint32(len(pdu)))
	copy(info[fixed:], pdu)
	return info
}

// SendSMS sends a GSM PDU and returns the network message reference.
func SendSMS(ctx context.Context, d *Device, pdu []byte) (uint32, error) {
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSSend, CommandTypeSet, encodeSMSSend(pdu))
	if err != nil {
		return 0, err
	}
	if resp.Status != 0 {
		return 0, fmt.Errorf("mbim: SMS_SEND status=%d", resp.Status)
	}
	if len(resp.InfoBuffer) < 4 {
		return 0, fmt.Errorf("mbim: SMS_SEND response too short len=%d", len(resp.InfoBuffer))
	}
	return le.Uint32(resp.InfoBuffer[0:]), nil
}

// SMSRecord is one decoded PDU SMS read record.
type SMSRecord struct {
	Index  uint32
	Status uint32
	PDU    []byte
}

func encodeSMSReadQuery(flag, index uint32) []byte {
	info := make([]byte, 12)
	le.PutUint32(info[0:], SMSFormatPDU)
	le.PutUint32(info[4:], flag)
	le.PutUint32(info[8:], index)
	return info
}

func parseSMSRead(info []byte) ([]SMSRecord, error) {
	if len(info) < 8 {
		return nil, fmt.Errorf("mbim: SMS_READ info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	count, _ := r.u32At(4)
	out := make([]SMSRecord, 0, count)
	for i := uint32(0); i < count; i++ {
		pairPos := 8 + int(i)*8
		recOff, err := r.u32At(pairPos)
		if err != nil {
			return nil, fmt.Errorf("mbim: SMS_READ record %d offset: %w", i, err)
		}
		recSize, err := r.u32At(pairPos + 4)
		if err != nil {
			return nil, fmt.Errorf("mbim: SMS_READ record %d size: %w", i, err)
		}
		if recSize < 16 || uint64(recOff)+uint64(recSize) > uint64(len(info)) {
			return nil, fmt.Errorf("mbim: SMS_READ record %d out of range off=%d size=%d", i, recOff, recSize)
		}
		rec := info[recOff : recOff+recSize]
		rr := newInfoReader(rec)
		var sr SMSRecord
		sr.Index, _ = rr.u32At(0)
		sr.Status, _ = rr.u32At(4)
		pduOff, _ := rr.u32At(8)
		pduSize, _ := rr.u32At(12)
		if uint64(pduOff)+uint64(pduSize) > uint64(len(rec)) {
			return nil, fmt.Errorf("mbim: SMS_READ record %d pdu out of range", i)
		}
		sr.PDU = append([]byte(nil), rec[pduOff:uint64(pduOff)+uint64(pduSize)]...)
		out = append(out, sr)
	}
	return out, nil
}

// ReadSMS reads the PDU message at the given storage index.
func ReadSMS(ctx context.Context, d *Device, index uint32) (SMSRecord, error) {
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSRead, CommandTypeQuery, encodeSMSReadQuery(SMSFlagIndex, index))
	if err != nil {
		return SMSRecord{}, err
	}
	if resp.Status != 0 {
		return SMSRecord{}, fmt.Errorf("mbim: SMS_READ status=%d", resp.Status)
	}
	recs, err := parseSMSRead(resp.InfoBuffer)
	if err != nil {
		return SMSRecord{}, err
	}
	if len(recs) == 0 {
		return SMSRecord{}, fmt.Errorf("mbim: SMS_READ index=%d no record", index)
	}
	return recs[0], nil
}

// ListSMS reads all stored PDU messages.
func ListSMS(ctx context.Context, d *Device) ([]SMSRecord, error) {
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSRead, CommandTypeQuery, encodeSMSReadQuery(SMSFlagAll, 0))
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("mbim: SMS_READ(all) status=%d", resp.Status)
	}
	return parseSMSRead(resp.InfoBuffer)
}

func deleteSMS(ctx context.Context, d *Device, flag, index uint32) error {
	info := make([]byte, 8)
	le.PutUint32(info[0:], flag)
	le.PutUint32(info[4:], index)
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSDelete, CommandTypeSet, info)
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return fmt.Errorf("mbim: SMS_DELETE status=%d", resp.Status)
	}
	return nil
}

// DeleteSMS deletes the message at the given storage index.
func DeleteSMS(ctx context.Context, d *Device, index uint32) error {
	return deleteSMS(ctx, d, SMSFlagIndex, index)
}

// DeleteAllSMS deletes all stored messages.
func DeleteAllSMS(ctx context.Context, d *Device) error {
	return deleteSMS(ctx, d, SMSFlagAll, 0)
}

const smsConfigSCAddressPair = 16

// GetSMSC returns the service-center address from SMS configuration.
func GetSMSC(ctx context.Context, d *Device) (string, error) {
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSConfiguration, CommandTypeQuery, nil)
	if err != nil {
		return "", err
	}
	if resp.Status != 0 {
		return "", fmt.Errorf("mbim: SMS_CONFIGURATION status=%d", resp.Status)
	}
	if len(resp.InfoBuffer) < smsConfigSCAddressPair+8 {
		return "", fmt.Errorf("mbim: SMS_CONFIGURATION info too short len=%d", len(resp.InfoBuffer))
	}
	return newInfoReader(resp.InfoBuffer).stringAt(smsConfigSCAddressPair)
}

func encodeSetSMSConfig(smsc string) []byte {
	const fixed = 12
	var sc []byte
	if smsc != "" {
		sc = encodeUTF16LE(smsc)
	}
	info := make([]byte, fixed+pad4(len(sc)))
	le.PutUint32(info[0:], SMSFormatPDU)
	if len(sc) > 0 {
		le.PutUint32(info[4:], fixed)
		le.PutUint32(info[8:], uint32(len(sc)))
		copy(info[fixed:], sc)
	}
	return info
}

// SetSMSC writes the service-center address into SMS configuration.
func SetSMSC(ctx context.Context, d *Device, smsc string) error {
	resp, err := d.Command(ctx, UUIDSMS, CIDSMSConfiguration, CommandTypeSet, encodeSetSMSConfig(smsc))
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return fmt.Errorf("mbim: SMS_CONFIGURATION set status=%d", resp.Status)
	}
	return nil
}
