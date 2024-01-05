package e2etest

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"

	sdkmath "cosmossdk.io/math"
	"github.com/babylonchain/babylon/testutil/datagen"
	bbntypes "github.com/babylonchain/babylon/types"
	btcctypes "github.com/babylonchain/babylon/x/btccheckpoint/types"
	btclctypes "github.com/babylonchain/babylon/x/btclightclient/types"
	bstypes "github.com/babylonchain/babylon/x/btcstaking/types"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/babylonchain/finality-provider/eotsmanager/client"
	eotsconfig "github.com/babylonchain/finality-provider/eotsmanager/config"
	fpcfg "github.com/babylonchain/finality-provider/finality-provider/config"
	"github.com/babylonchain/finality-provider/finality-provider/service"

	fpcc "github.com/babylonchain/finality-provider/clientcontroller"

	covcc "github.com/babylonchain/covenant-emulator/clientcontroller"
	covcfg "github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/covenant"
	"github.com/babylonchain/covenant-emulator/types"
)

var (
	eventuallyWaitTimeOut = 1 * time.Minute
	eventuallyPollTime    = 500 * time.Millisecond
	btcNetworkParams      = &chaincfg.SimNetParams

	fpNamePrefix    = "test-fp-"
	monikerPrefix   = "moniker-"
	covenantKeyName = "covenant-key"
	chainID         = "chain-test"
	passphrase      = "testpass"
	hdPath          = ""
)

type TestManager struct {
	Wg                sync.WaitGroup
	BabylonHandler    *BabylonNodeHandler
	EOTSServerHandler *EOTSServerHandler
	CovenantEmulator  *covenant.CovenantEmulator
	FpConfig          *fpcfg.Config
	EOTSConfig        *eotsconfig.Config
	CovenanConfig     *covcfg.Config
	Fpa               *service.FinalityProviderApp
	EOTSClient        *client.EOTSManagerGRpcClient
	FPBBNClient       *fpcc.BabylonController
	CovBBNClient      *covcc.BabylonController
	baseDir           string
}

type TestDelegationData struct {
	DelegatorPrivKey        *btcec.PrivateKey
	DelegatorKey            *btcec.PublicKey
	DelegatorBabylonPrivKey *secp256k1.PrivKey
	DelegatorBabylonKey     *secp256k1.PubKey
	SlashingTx              *bstypes.BTCSlashingTx
	StakingTx               *wire.MsgTx
	StakingTxInfo           *btcctypes.TransactionInfo
	DelegatorSig            *bbntypes.BIP340Signature
	FpPks                   []*btcec.PublicKey

	SlashingAddr  string
	ChangeAddr    string
	StakingTime   uint16
	StakingAmount int64
}

func StartManager(t *testing.T) *TestManager {
	testDir, err := baseDir("cee2etest")
	require.NoError(t, err)

	logger := zap.NewNop()

	// 1. prepare covenant key, which will be used as input of Babylon node
	covenantConfig := defaultCovenantConfig(testDir)
	err = covenantConfig.Validate()
	require.NoError(t, err)
	covKeyPair, err := covenant.CreateCovenantKey(testDir, chainID, covenantKeyName, keyring.BackendTest, passphrase, hdPath)
	require.NoError(t, err)

	// 2. prepare Babylon node
	bh := NewBabylonNodeHandler(t, bbntypes.NewBIP340PubKeyFromBTCPK(covKeyPair.PublicKey))
	err = bh.Start()
	require.NoError(t, err)
	fpHomeDir := filepath.Join(testDir, "fp-home")
	cfg := defaultFpConfig(bh.GetNodeDataDir(), fpHomeDir)
	fpbc, err := fpcc.NewBabylonController(cfg.BabylonConfig, &cfg.BTCNetParams, logger)
	require.NoError(t, err)

	// 3. prepare EOTS manager
	eotsHomeDir := filepath.Join(testDir, "eots-home")
	eotsCfg := defaultEOTSConfig()
	eh := NewEOTSServerHandler(t, eotsCfg, eotsHomeDir)
	eh.Start()
	eotsCli, err := client.NewEOTSManagerGRpcClient(cfg.EOTSManagerAddress)
	require.NoError(t, err)

	// 4. prepare finality-provider
	fpApp, err := service.NewFinalityProviderApp(fpHomeDir, cfg, fpbc, eotsCli, logger)
	require.NoError(t, err)
	err = fpApp.Start()
	require.NoError(t, err)

	// 5. prepare covenant emulator
	bbnCfg := defaultBBNConfigWithKey(cfg.BabylonConfig.Key, cfg.BabylonConfig.KeyDirectory)
	covbc, err := covcc.NewBabylonController(bbnCfg, &covenantConfig.BTCNetParams, logger)
	require.NoError(t, err)
	ce, err := covenant.NewCovenantEmulator(covenantConfig, covbc, passphrase, logger)
	require.NoError(t, err)
	err = ce.Start()
	require.NoError(t, err)

	tm := &TestManager{
		BabylonHandler:    bh,
		EOTSServerHandler: eh,
		FpConfig:          cfg,
		EOTSConfig:        eotsCfg,
		Fpa:               fpApp,
		CovenantEmulator:  ce,
		CovenanConfig:     covenantConfig,
		EOTSClient:        eotsCli,
		FPBBNClient:       fpbc,
		CovBBNClient:      covbc,
		baseDir:           testDir,
	}

	tm.WaitForServicesStart(t)

	return tm
}

func (tm *TestManager) WaitForServicesStart(t *testing.T) {
	// wait for Babylon node starts
	require.Eventually(t, func() bool {
		_, err := tm.CovBBNClient.QueryStakingParams()

		return err == nil
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("Babylon node is started")
}

func StartManagerWithFinalityProvider(t *testing.T, n int) (*TestManager, []*service.FinalityProviderInstance) {
	tm := StartManager(t)
	app := tm.Fpa

	for i := 0; i < n; i++ {
		fpName := fpNamePrefix + strconv.Itoa(i)
		moniker := monikerPrefix + strconv.Itoa(i)
		commission := sdkmath.LegacyZeroDec()
		desc, err := newDescription(moniker).Marshal()
		require.NoError(t, err)
		cfg := app.GetConfig()
		_, err = service.CreateChainKey(cfg.BabylonConfig.KeyDirectory, cfg.BabylonConfig.ChainID, fpName, keyring.BackendTest, passphrase, hdPath)
		require.NoError(t, err)
		res, err := app.CreateFinalityProvider(fpName, chainID, passphrase, hdPath, desc, &commission)
		require.NoError(t, err)
		fpPk, err := bbntypes.NewBIP340PubKey(res.StoreFp.BtcPk)
		require.NoError(t, err)
		_, err = app.RegisterFinalityProvider(fpPk.MarshalHex())
		require.NoError(t, err)
		err = app.StartHandlingFinalityProvider(fpPk, passphrase)
		require.NoError(t, err)
		fpIns, err := app.GetFinalityProviderInstance(fpPk)
		require.NoError(t, err)
		require.True(t, fpIns.IsRunning())
		require.NoError(t, err)

		// check finality providers on Babylon side
		require.Eventually(t, func() bool {
			fps, err := tm.FPBBNClient.QueryFinalityProviders()
			if err != nil {
				t.Logf("failed to query finality providers from Babylon %s", err.Error())
				return false
			}

			if len(fps) != i+1 {
				return false
			}

			for _, fp := range fps {
				if !strings.Contains(fp.Description.Moniker, monikerPrefix) {
					return false
				}
				if !fp.Commission.Equal(sdkmath.LegacyZeroDec()) {
					return false
				}
			}

			return true
		}, eventuallyWaitTimeOut, eventuallyPollTime)
	}

	fpInsList := app.ListFinalityProviderInstances()
	require.Equal(t, n, len(fpInsList))

	t.Logf("the test manager is running with %v finality-provider(s)", len(fpInsList))

	return tm, fpInsList
}

func (tm *TestManager) Stop(t *testing.T) {
	err := tm.Fpa.Stop()
	require.NoError(t, err)
	err = tm.CovenantEmulator.Stop()
	require.NoError(t, err)
	err = tm.BabylonHandler.Stop()
	require.NoError(t, err)
	err = os.RemoveAll(tm.baseDir)
	require.NoError(t, err)
	tm.EOTSServerHandler.Stop()
}

func (tm *TestManager) WaitForFpRegistered(t *testing.T, bbnPk *secp256k1.PubKey) {
	require.Eventually(t, func() bool {
		queriedFps, err := tm.FPBBNClient.QueryFinalityProviders()
		if err != nil {
			return false
		}
		return len(queriedFps) == 1 && queriedFps[0].BabylonPk.Equals(bbnPk)
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("the finality-provider is successfully registered")
}

func (tm *TestManager) WaitForFpPubRandCommitted(t *testing.T, fpIns *service.FinalityProviderInstance) {
	require.Eventually(t, func() bool {
		lastCommittedHeight, err := fpIns.GetLastCommittedHeight()
		if err != nil {
			return false
		}
		return lastCommittedHeight > 0
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("public randomness is successfully committed")
}

func (tm *TestManager) WaitForNPendingDels(t *testing.T, n int) []*types.Delegation {
	var (
		dels []*types.Delegation
		err  error
	)
	require.Eventually(t, func() bool {
		dels, err = tm.CovBBNClient.QueryPendingDelegations(
			tm.CovenanConfig.DelegationLimit,
		)
		if err != nil {
			return false
		}
		return len(dels) == n
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("delegations are pending")

	return dels
}

func (tm *TestManager) WaitForFpNActiveDels(t *testing.T, btcPk *bbntypes.BIP340PubKey, n int) []*types.Delegation {
	var dels []*types.Delegation
	currentBtcTip, err := tm.FPBBNClient.QueryBtcLightClientTip()
	require.NoError(t, err)
	params, err := tm.CovBBNClient.QueryStakingParams()
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		dels, err = tm.CovBBNClient.QueryFinalityProviderDelegations(btcPk, 1000)
		if err != nil {
			return false
		}
		return len(dels) == n && CheckDelsStatus(dels, currentBtcTip.Height, params.FinalizationTimeoutBlocks,
			params.CovenantQuorum, bstypes.BTCDelegationStatus_ACTIVE)
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	t.Logf("the delegation is active, finality providers should start voting")

	return dels
}

func CheckDelsStatus(dels []*types.Delegation, btcHeight uint64, w uint64, covenantQuorum uint32, status bstypes.BTCDelegationStatus) bool {
	allChecked := true
	for _, d := range dels {
		s := getDelStatus(d, btcHeight, w, covenantQuorum)
		if s != status {
			allChecked = false
		}
	}

	return allChecked
}

func getDelStatus(del *types.Delegation, btcHeight uint64, w uint64, covenantQuorum uint32) bstypes.BTCDelegationStatus {
	if del.BtcUndelegation.DelegatorUnbondingSig != nil {
		// this means the delegator has signed unbonding signature, and Babylon will consider
		// this BTC delegation unbonded directly
		return bstypes.BTCDelegationStatus_UNBONDED
	}

	if btcHeight < del.StartHeight || btcHeight+w > del.EndHeight {
		// staking tx's timelock has not begun, or is less than w BTC
		// blocks left, or is expired
		return bstypes.BTCDelegationStatus_UNBONDED
	}

	// at this point, BTC delegation has an active timelock, and Babylon is not
	// aware of unbonding tx with delegator's signature
	if del.HasCovenantQuorum(covenantQuorum) {
		return bstypes.BTCDelegationStatus_ACTIVE
	}

	// no covenant quorum yet, pending
	return bstypes.BTCDelegationStatus_PENDING
}

func (tm *TestManager) CheckBlockFinalization(t *testing.T, height uint64, num int) {
	// we need to ensure votes are collected at the given height
	require.Eventually(t, func() bool {
		votes, err := tm.FPBBNClient.QueryVotesAtHeight(height)
		if err != nil {
			t.Logf("failed to get the votes at height %v: %s", height, err.Error())
			return false
		}
		return len(votes) == num
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	// as the votes have been collected, the block should be finalized
	require.Eventually(t, func() bool {
		b, err := tm.FPBBNClient.QueryBlock(height)
		if err != nil {
			t.Logf("failed to query block at height %v: %s", height, err.Error())
			return false
		}
		return b.Finalized
	}, eventuallyWaitTimeOut, eventuallyPollTime)
}

func (tm *TestManager) WaitForFpVoteCast(t *testing.T, fpIns *service.FinalityProviderInstance) uint64 {
	var lastVotedHeight uint64
	require.Eventually(t, func() bool {
		if fpIns.GetLastVotedHeight() > 0 {
			lastVotedHeight = fpIns.GetLastVotedHeight()
			return true
		} else {
			return false
		}
	}, eventuallyWaitTimeOut, eventuallyPollTime)

	return lastVotedHeight
}

func (tm *TestManager) InsertBTCDelegation(t *testing.T, fpPks []*btcec.PublicKey, stakingTime uint16, stakingAmount int64, params *types.StakingParams) *TestDelegationData {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	// delegator BTC key pairs, staking tx and slashing tx
	delBtcPrivKey, delBtcPubKey, err := datagen.GenRandomBTCKeyPair(r)
	require.NoError(t, err)

	changeAddress, err := datagen.GenRandomBTCAddress(r, btcNetworkParams)
	require.NoError(t, err)

	slashingLockTime := uint16(101)
	testStakingInfo := datagen.GenBTCStakingSlashingInfo(
		r,
		t,
		btcNetworkParams,
		delBtcPrivKey,
		fpPks,
		params.CovenantPks,
		params.CovenantQuorum,
		stakingTime,
		stakingAmount,
		params.SlashingAddress.String(),
		params.SlashingRate,
		slashingLockTime,
	)

	// delegator Babylon key pairs
	delBabylonPrivKey, delBabylonPubKey, err := datagen.GenRandomSecp256k1KeyPair(r)
	require.NoError(t, err)

	// proof-of-possession
	pop, err := bstypes.NewPoP(delBabylonPrivKey, delBtcPrivKey)
	require.NoError(t, err)

	// create and insert BTC headers which include the staking tx to get staking tx info
	currentBtcTip, err := tm.FPBBNClient.QueryBtcLightClientTip()
	require.NoError(t, err)
	blockWithStakingTx := datagen.CreateBlockWithTransaction(r, currentBtcTip.Header.ToBlockHeader(), testStakingInfo.StakingTx)
	accumulatedWork := btclctypes.CalcWork(&blockWithStakingTx.HeaderBytes)
	accumulatedWork = btclctypes.CumulativeWork(accumulatedWork, *currentBtcTip.Work)
	parentBlockHeaderInfo := &btclctypes.BTCHeaderInfo{
		Header: &blockWithStakingTx.HeaderBytes,
		Hash:   blockWithStakingTx.HeaderBytes.Hash(),
		Height: currentBtcTip.Height + 1,
		Work:   &accumulatedWork,
	}
	headers := make([]bbntypes.BTCHeaderBytes, 0)
	headers = append(headers, blockWithStakingTx.HeaderBytes)
	for i := 0; i < int(params.ComfirmationTimeBlocks); i++ {
		headerInfo := datagen.GenRandomValidBTCHeaderInfoWithParent(r, *parentBlockHeaderInfo)
		headers = append(headers, *headerInfo.Header)
		parentBlockHeaderInfo = headerInfo
	}
	_, err = tm.FPBBNClient.InsertBtcBlockHeaders(headers)
	require.NoError(t, err)
	btcHeader := blockWithStakingTx.HeaderBytes
	serializedStakingTx, err := bbntypes.SerializeBTCTx(testStakingInfo.StakingTx)
	require.NoError(t, err)
	txInfo := btcctypes.NewTransactionInfo(&btcctypes.TransactionKey{Index: 1, Hash: btcHeader.Hash()}, serializedStakingTx, blockWithStakingTx.SpvProof.MerkleNodes)

	slashignSpendInfo, err := testStakingInfo.StakingInfo.SlashingPathSpendInfo()
	require.NoError(t, err)

	// delegator sig
	delegatorSig, err := testStakingInfo.SlashingTx.Sign(
		testStakingInfo.StakingTx,
		0,
		slashignSpendInfo.GetPkScriptPath(),
		delBtcPrivKey,
	)
	require.NoError(t, err)

	unbondingTime := uint16(params.FinalizationTimeoutBlocks) + 1
	unbondingValue := stakingAmount - 1000
	stakingTxHash := testStakingInfo.StakingTx.TxHash()

	testUnbondingInfo := datagen.GenBTCUnbondingSlashingInfo(
		r,
		t,
		btcNetworkParams,
		delBtcPrivKey,
		fpPks,
		params.CovenantPks,
		params.CovenantQuorum,
		wire.NewOutPoint(&stakingTxHash, 0),
		unbondingTime,
		unbondingValue,
		params.SlashingAddress.String(),
		params.SlashingRate,
		slashingLockTime,
	)

	unbondingTxMsg := testUnbondingInfo.UnbondingTx

	unbondingSlashingPathInfo, err := testUnbondingInfo.UnbondingInfo.SlashingPathSpendInfo()
	require.NoError(t, err)

	unbondingSig, err := testUnbondingInfo.SlashingTx.Sign(
		unbondingTxMsg,
		0,
		unbondingSlashingPathInfo.GetPkScriptPath(),
		delBtcPrivKey,
	)
	require.NoError(t, err)

	serializedUnbondingTx, err := bbntypes.SerializeBTCTx(testUnbondingInfo.UnbondingTx)
	require.NoError(t, err)

	// submit the BTC delegation to Babylon
	_, err = tm.FPBBNClient.CreateBTCDelegation(
		delBabylonPubKey.(*secp256k1.PubKey),
		bbntypes.NewBIP340PubKeyFromBTCPK(delBtcPubKey),
		fpPks,
		pop,
		uint32(stakingTime),
		stakingAmount,
		txInfo,
		testStakingInfo.SlashingTx,
		delegatorSig,
		serializedUnbondingTx,
		uint32(unbondingTime),
		unbondingValue,
		testUnbondingInfo.SlashingTx,
		unbondingSig)
	require.NoError(t, err)

	t.Log("successfully submitted a BTC delegation")

	return &TestDelegationData{
		DelegatorPrivKey:        delBtcPrivKey,
		DelegatorKey:            delBtcPubKey,
		DelegatorBabylonPrivKey: delBabylonPrivKey.(*secp256k1.PrivKey),
		DelegatorBabylonKey:     delBabylonPubKey.(*secp256k1.PubKey),
		FpPks:                   fpPks,
		StakingTx:               testStakingInfo.StakingTx,
		SlashingTx:              testStakingInfo.SlashingTx,
		StakingTxInfo:           txInfo,
		DelegatorSig:            delegatorSig,
		SlashingAddr:            params.SlashingAddress.String(),
		ChangeAddr:              changeAddress.String(),
		StakingTime:             stakingTime,
		StakingAmount:           stakingAmount,
	}
}

func (tm *TestManager) GetParams(t *testing.T) *types.StakingParams {
	p, err := tm.CovBBNClient.QueryStakingParams()
	require.NoError(t, err)
	return p
}

func defaultFpConfig(keyringDir, homeDir string) *fpcfg.Config {
	cfg := fpcfg.DefaultConfigWithHome(homeDir)

	cfg.PollerConfig.AutoChainScanningMode = false
	// babylon configs for sending transactions
	cfg.BabylonConfig.KeyDirectory = keyringDir
	// need to use this one to send otherwise we will have account sequence mismatch
	// errors
	cfg.BabylonConfig.Key = "test-spending-key"
	// Big adjustment to make sure we have enough gas in our transactions
	cfg.BabylonConfig.GasAdjustment = 20
	cfg.UnbondingSigSubmissionInterval = 3 * time.Second

	return &cfg
}

func defaultBBNConfigWithKey(key, keydir string) *covcfg.BBNConfig {
	bbnCfg := covcfg.DefaultBBNConfig()
	bbnCfg.Key = key
	bbnCfg.KeyDirectory = keydir
	bbnCfg.GasAdjustment = 20

	return &bbnCfg
}

func defaultCovenantConfig(homeDir string) *covcfg.Config {
	cfg := covcfg.DefaultConfigWithHomePath(homeDir)
	cfg.BabylonConfig.KeyDirectory = homeDir

	return &cfg
}

func defaultEOTSConfig() *eotsconfig.Config {
	cfg := eotsconfig.DefaultConfig()

	return &cfg
}

func newDescription(moniker string) *stakingtypes.Description {
	dec := stakingtypes.NewDescription(moniker, "", "", "", "")
	return &dec
}
