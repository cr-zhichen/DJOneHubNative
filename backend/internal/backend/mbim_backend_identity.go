package backend

import "context"

// Live identity readers — satisfy device.liveSIMIdentityReader / liveSIMSPNReader /
// liveSIMMetadataReader so the worker's refreshIdentityLive populates ICCID/IMSI/
// SPN/metadata for MBIM devices. For MBIM every query is already a live
// SubscriberReady, so these delegate to the non-Live variants.

func (b *MBIMBackend) GetIMSILive(ctx context.Context) (string, error) {
	return b.GetIMSI(ctx)
}

func (b *MBIMBackend) GetICCIDLive(ctx context.Context) (string, error) {
	return b.GetICCID(ctx)
}

func (b *MBIMBackend) GetNativeSPNLive(ctx context.Context) (string, error) {
	return b.GetNativeSPN(ctx)
}

func (b *MBIMBackend) GetSIMMetadataLive(ctx context.Context) (*SIMMetadata, error) {
	return b.GetSIMMetadata(ctx)
}
