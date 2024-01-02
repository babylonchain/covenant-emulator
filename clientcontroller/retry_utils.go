package clientcontroller

import (
	"errors"

	sdkErr "cosmossdk.io/errors"
	btcstakingtypes "github.com/babylonchain/babylon/x/btcstaking/types"
	finalitytypes "github.com/babylonchain/babylon/x/finality/types"
)

// these errors are considered unrecoverable because these indicate
// something critical in the finality provider program or the consumer chain
var unrecoverableErrors = []*sdkErr.Error{
	finalitytypes.ErrBlockNotFound,
	finalitytypes.ErrInvalidFinalitySig,
	finalitytypes.ErrHeightTooHigh,
	finalitytypes.ErrInvalidPubRand,
	finalitytypes.ErrNoPubRandYet,
	finalitytypes.ErrPubRandNotFound,
	finalitytypes.ErrTooFewPubRand,
	btcstakingtypes.ErrFpAlreadySlashed,
}

// IsUnrecoverable returns true when the error is in the unrecoverableErrors list
func IsUnrecoverable(err error) bool {
	for _, e := range unrecoverableErrors {
		if errors.Is(err, e) {
			return true
		}
	}

	return false
}

var expectedErrors = []*sdkErr.Error{
	// if due to some low-level reason (e.g., network), we submit duplicated finality sig,
	// we should just ignore the error
	finalitytypes.ErrDuplicatedFinalitySig,
}

// IsExpected returns true when the error is in the expectedErrors list
func IsExpected(err error) bool {
	for _, e := range expectedErrors {
		if errors.Is(err, e) {
			return true
		}
	}

	return false
}
