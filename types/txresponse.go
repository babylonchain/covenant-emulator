package types

import (
	"github.com/cosmos/relayer/v2/relayer/provider"
)

type TxResponse struct {
	TxHash string
	Events []provider.RelayerEvent
}
