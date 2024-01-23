package e2etest

import (
	"testing"
)

var (
	stakingTime   = uint16(100)
	stakingAmount = int64(20000)
)

// TestCovenantEmulatorLifeCycle tests the whole life cycle of a finality-provider
// creation -> registration -> randomness commitment ->
// activation with BTC delegation and Covenant sig ->
// vote submission -> block finalization
func TestCovenantEmulatorLifeCycle(t *testing.T) {
	tm, btcPks := StartManagerWithFinalityProvider(t, 1)
	defer tm.Stop(t)

	// send a BTC delegation
	_ = tm.InsertBTCDelegation(t, btcPks, stakingTime, stakingAmount)

	// check the BTC delegation is pending
	_ = tm.WaitForNPendingDels(t, 1)

	// check the BTC delegation is active
	_ = tm.WaitForNActiveDels(t, 1)
}
