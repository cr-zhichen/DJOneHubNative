package mbim

import (
	"context"
	"fmt"
)

// Provider is one entry from the BASIC_CONNECT VISIBLE_PROVIDERS response.
type Provider struct {
	PLMN          string
	Name          string
	State         uint32
	CellularClass uint32
	RSSI          uint32
}

const (
	visibleProvidersActionFullScan = 0
	providerRefLen                 = 8
	providerFixedLen               = 32
)

func parseVisibleProviders(info []byte) ([]Provider, error) {
	r := newInfoReader(info)
	count, err := r.u32At(0)
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, count)
	for i := uint32(0); i < count; i++ {
		refPos := 4 + int(i)*providerRefLen
		offset, err := r.u32At(refPos)
		if err != nil {
			return nil, fmt.Errorf("mbim: visible provider %d offset: %w", i, err)
		}
		size, err := r.u32At(refPos + 4)
		if err != nil {
			return nil, fmt.Errorf("mbim: visible provider %d size: %w", i, err)
		}
		if size < providerFixedLen || uint64(offset)+uint64(size) > uint64(len(info)) {
			return nil, fmt.Errorf("mbim: visible provider %d out of range off=%d size=%d", i, offset, size)
		}
		provider, err := parseProvider(info[offset : uint64(offset)+uint64(size)])
		if err != nil {
			return nil, fmt.Errorf("mbim: visible provider %d: %w", i, err)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}

func parseProvider(info []byte) (Provider, error) {
	r := newInfoReader(info)
	idOffset, _ := r.u32At(0)
	idLength, _ := r.u32At(4)
	state, _ := r.u32At(8)
	nameOffset, _ := r.u32At(12)
	nameLength, _ := r.u32At(16)
	class, _ := r.u32At(20)
	rssi, _ := r.u32At(24)
	plmn, err := decodeUTF16Range(info, idOffset, idLength)
	if err != nil {
		return Provider{}, fmt.Errorf("parse ProviderId: %w", err)
	}
	name, err := decodeUTF16Range(info, nameOffset, nameLength)
	if err != nil {
		return Provider{}, fmt.Errorf("parse ProviderName: %w", err)
	}
	return Provider{
		PLMN:          plmn,
		Name:          name,
		State:         state,
		CellularClass: class,
		RSSI:          rssi,
	}, nil
}

// QueryHomeProvider issues HOME_PROVIDER and parses the single MBIM_PROVIDER.
// 它的 ProviderId(PLMN)由模组按正确的 MNC 长度给出(5 或 6 位),
// 适合作为"原运营商"的权威来源,避免靠 IMSI 猜 MNC 长度。
func QueryHomeProvider(ctx context.Context, d *Device) (Provider, error) {
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectHomeProvider, CommandTypeQuery, nil)
	if err != nil {
		return Provider{}, err
	}
	if resp.Status != 0 {
		return Provider{}, fmt.Errorf("mbim: HOME_PROVIDER status=%d", resp.Status)
	}
	return parseProvider(resp.InfoBuffer)
}

// QueryVisibleProviders issues VISIBLE_PROVIDERS and parses the response.
func QueryVisibleProviders(ctx context.Context, d *Device) ([]Provider, error) {
	info := make([]byte, 4)
	le.PutUint32(info[0:], visibleProvidersActionFullScan)
	resp, err := d.Command(ctx, UUIDBasicConnect, CIDBasicConnectVisibleProviders, CommandTypeQuery, info)
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("mbim: VISIBLE_PROVIDERS status=%d", resp.Status)
	}
	return parseVisibleProviders(resp.InfoBuffer)
}
