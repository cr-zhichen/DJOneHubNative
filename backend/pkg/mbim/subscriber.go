package mbim

import (
	"context"
	"fmt"
)

// SubscriberReady is the parsed Subscriber Ready Status response.
type SubscriberReady struct {
	ReadyState uint32
	IMSI       string
	ICCID      string
	MSISDN     string
}

const subscriberFixedLen = 4 + 8 + 8 + 4 + 4

func parseSubscriberReady(info []byte) (SubscriberReady, error) {
	if len(info) < subscriberFixedLen {
		return SubscriberReady{}, fmt.Errorf("mbim: subscriber info too short len=%d", len(info))
	}
	r := newInfoReader(info)
	var s SubscriberReady
	s.ReadyState, _ = r.u32At(0)
	var err error
	if s.IMSI, err = r.stringAt(4); err != nil {
		return SubscriberReady{}, fmt.Errorf("mbim: parse IMSI: %w", err)
	}
	if s.ICCID, err = r.stringAt(12); err != nil {
		return SubscriberReady{}, fmt.Errorf("mbim: parse ICCID: %w", err)
	}
	numbers, err := r.stringArrayCountAt(24)
	if err != nil {
		return SubscriberReady{}, fmt.Errorf("mbim: parse telephone numbers: %w", err)
	}
	if len(numbers) > 0 {
		s.MSISDN = numbers[0]
	}
	return s, nil
}

// QuerySubscriberReady issues SUBSCRIBER_READY_STATUS and parses the response.
func QuerySubscriberReady(ctx context.Context, d *Device) (SubscriberReady, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectSubscriberReadyStatus, CommandTypeQuery, nil)
	if err != nil {
		return SubscriberReady{}, err
	}
	if resp.Status != 0 {
		return SubscriberReady{}, fmt.Errorf("mbim: SUBSCRIBER_READY status=%d", resp.Status)
	}
	return parseSubscriberReady(resp.InfoBuffer)
}
