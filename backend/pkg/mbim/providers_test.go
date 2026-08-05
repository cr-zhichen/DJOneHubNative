package mbim

import (
	"context"
	"testing"
)

func TestParseVisibleProvidersOneProvider(t *testing.T) {
	info := buildVisibleProvidersInfoForTest([]Provider{{
		PLMN:          "310260",
		Name:          "T-Mobile",
		State:         2,
		CellularClass: CellularClassGSM,
		RSSI:          21,
	}})

	providers, err := parseVisibleProviders(info)
	if err != nil {
		t.Fatalf("parseVisibleProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(providers))
	}
	got := providers[0]
	if got.PLMN != "310260" || got.Name != "T-Mobile" || got.State != 2 || got.CellularClass != CellularClassGSM || got.RSSI != 21 {
		t.Fatalf("provider = %+v", got)
	}
}

func TestQueryVisibleProvidersReturnsZeroProviders(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			if le.Uint32(w[36:]) != CIDBasicConnectVisibleProviders {
				t.Fatalf("CID = %d, want %d", le.Uint32(w[36:]), CIDBasicConnectVisibleProviders)
			}
			if CommandType(le.Uint32(w[40:])) != CommandTypeQuery {
				t.Fatalf("command type = %d, want query", le.Uint32(w[40:]))
			}
			if got := le.Uint32(w[44:]); got != 4 {
				t.Fatalf("query info len = %d, want 4", got)
			}
			if got := le.Uint32(w[48:]); got != 0 {
				t.Fatalf("visible providers action = %d, want full scan 0", got)
			}
			info := make([]byte, 4)
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectVisibleProviders, info), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	providers, err := QueryVisibleProviders(context.Background(), d)
	if err != nil {
		t.Fatalf("QueryVisibleProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("providers len = %d, want 0", len(providers))
	}
}

func TestQueryHomeProviderParsesFullLengthMNC(t *testing.T) {
	ft := newFakeTransport()
	ft.reply = func(w []byte) ([]byte, bool) {
		h, _ := decodeHeader(w)
		switch h.Type {
		case MessageTypeOpen:
			return openDoneMsg(h.TransactionID), true
		case MessageTypeCommand:
			if le.Uint32(w[36:]) != CIDBasicConnectHomeProvider {
				t.Fatalf("CID = %d, want HomeProvider %d", le.Uint32(w[36:]), CIDBasicConnectHomeProvider)
			}
			// 3 位 MNC 的家网络 PLMN(美国 310/840)。
			info := buildSingleProviderInfoForTest(Provider{PLMN: "310840", Name: "Home", State: 1, CellularClass: CellularClassGSM})
			return makeCommandDoneFragmentFor(h.TransactionID, UUIDBasicConnect, CIDBasicConnectHomeProvider, info), true
		}
		return nil, false
	}
	d := newDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	p, err := QueryHomeProvider(context.Background(), d)
	if err != nil {
		t.Fatalf("QueryHomeProvider: %v", err)
	}
	if p.PLMN != "310840" {
		t.Fatalf("home provider PLMN = %q, want 310840", p.PLMN)
	}
}

// buildSingleProviderInfoForTest 构造一个裸 MBIM_PROVIDER 结构(HomeProvider 的 InfoBuffer 形态)。
func buildSingleProviderInfoForTest(p Provider) []byte {
	const providerFixedLen = 32
	plmn := encodeUTF16(p.PLMN)
	name := encodeUTF16(p.Name)
	provider := make([]byte, providerFixedLen+len(plmn)+len(name))
	le.PutUint32(provider[0:], providerFixedLen)
	le.PutUint32(provider[4:], uint32(len(plmn)))
	copy(provider[providerFixedLen:], plmn)
	le.PutUint32(provider[8:], p.State)
	nameOffset := providerFixedLen + len(plmn)
	le.PutUint32(provider[12:], uint32(nameOffset))
	le.PutUint32(provider[16:], uint32(len(name)))
	copy(provider[nameOffset:], name)
	le.PutUint32(provider[20:], p.CellularClass)
	le.PutUint32(provider[24:], p.RSSI)
	return provider
}

func buildVisibleProvidersInfoForTest(providers []Provider) []byte {
	const providerRefLen = 8
	const providerFixedLen = 32
	info := make([]byte, 4+len(providers)*providerRefLen)
	le.PutUint32(info[0:], uint32(len(providers)))
	for i, p := range providers {
		plmn := encodeUTF16(p.PLMN)
		name := encodeUTF16(p.Name)
		providerOffset := len(info)
		provider := make([]byte, providerFixedLen+len(plmn)+len(name))
		le.PutUint32(provider[0:], providerFixedLen)
		le.PutUint32(provider[4:], uint32(len(plmn)))
		copy(provider[providerFixedLen:], plmn)
		le.PutUint32(provider[8:], p.State)
		nameOffset := providerFixedLen + len(plmn)
		le.PutUint32(provider[12:], uint32(nameOffset))
		le.PutUint32(provider[16:], uint32(len(name)))
		copy(provider[nameOffset:], name)
		le.PutUint32(provider[20:], p.CellularClass)
		le.PutUint32(provider[24:], p.RSSI)
		info = append(info, provider...)
		ref := 4 + i*providerRefLen
		le.PutUint32(info[ref:], uint32(providerOffset))
		le.PutUint32(info[ref+4:], uint32(len(provider)))
	}
	return info
}
