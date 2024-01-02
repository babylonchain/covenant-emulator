//go:build e2e
// +build e2e

package e2etest

import (
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
)

var (
	stakingTime   = uint16(100)
	stakingAmount = int64(20000)
)

// TestFinalityProviderLifeCycle tests the whole life cycle of a finality-provider
// creation -> registration -> randomness commitment ->
// activation with BTC delegation and Covenant sig ->
// vote submission -> block finalization
func TestFinalityProviderLifeCycle(t *testing.T) {
	tm, fpInsList := StartManagerWithFinalityProvider(t, 1)
	defer tm.Stop(t)

	fpIns := fpInsList[0]

	params := tm.GetParams(t)

	// check the public randomness is committed
	tm.WaitForFpPubRandCommitted(t, fpIns)

	// send a BTC delegation
	_ = tm.InsertBTCDelegation(t, []*btcec.PublicKey{fpIns.MustGetBtcPk()}, stakingTime, stakingAmount, params)

	// check the BTC delegation is pending
	_ = tm.WaitForNPendingDels(t, 1)

	// check the BTC delegation is active
	_ = tm.WaitForFpNActiveDels(t, fpIns.GetBtcPkBIP340(), 1)

	// check the last voted block is finalized
	lastVotedHeight := tm.WaitForFpVoteCast(t, fpIns)
	tm.CheckBlockFinalization(t, lastVotedHeight, 1)
	t.Logf("the block at height %v is finalized", lastVotedHeight)
}
