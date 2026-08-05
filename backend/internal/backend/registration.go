package backend

import "context"

type PacketServiceController interface {
	AttachPacketService(ctx context.Context) error
	DetachPacketService(ctx context.Context) error
}
