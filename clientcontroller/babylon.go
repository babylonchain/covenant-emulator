package clientcontroller

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/btcsuite/btcd/btcec/v2"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	bbntypes "github.com/babylonchain/babylon/types"
	btcctypes "github.com/babylonchain/babylon/x/btccheckpoint/types"
	btclctypes "github.com/babylonchain/babylon/x/btclightclient/types"
	btcstakingtypes "github.com/babylonchain/babylon/x/btcstaking/types"
	finalitytypes "github.com/babylonchain/babylon/x/finality/types"
	bbnclient "github.com/babylonchain/rpc-client/client"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	sdkclient "github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkquery "github.com/cosmos/cosmos-sdk/types/query"
	"github.com/cosmos/relayer/v2/relayer/provider"
	"go.uber.org/zap"

	"github.com/babylonchain/covenant-emulator/config"
	"github.com/babylonchain/covenant-emulator/types"
)

var _ ClientController = &BabylonController{}

type BabylonController struct {
	bbnClient *bbnclient.Client
	cfg       *config.BBNConfig
	btcParams *chaincfg.Params
	logger    *zap.Logger
}

func NewBabylonController(
	cfg *config.BBNConfig,
	btcParams *chaincfg.Params,
	logger *zap.Logger,
) (*BabylonController, error) {

	bbnConfig := config.BBNConfigToBabylonConfig(cfg)

	if err := bbnConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config for Babylon client: %w", err)
	}

	bc, err := bbnclient.New(
		&bbnConfig,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Babylon client: %w", err)
	}

	return &BabylonController{
		bc,
		cfg,
		btcParams,
		logger,
	}, nil
}

func (bc *BabylonController) mustGetTxSigner() string {
	signer := bc.GetKeyAddress()
	prefix := bc.cfg.AccountPrefix
	return sdk.MustBech32ifyAddressBytes(prefix, signer)
}

func (bc *BabylonController) GetKeyAddress() sdk.AccAddress {
	// get key address, retrieves address based on key name which is configured in
	// cfg *stakercfg.BBNConfig. If this fails, it means we have misconfiguration problem
	// and we should panic.
	// This is checked at the start of BabylonController, so if it fails something is really wrong

	keyRec, err := bc.bbnClient.GetKeyring().Key(bc.cfg.Key)

	if err != nil {
		panic(fmt.Sprintf("Failed to get key address: %s", err))
	}

	addr, err := keyRec.GetAddress()

	if err != nil {
		panic(fmt.Sprintf("Failed to get key address: %s", err))
	}

	return addr
}

func (bc *BabylonController) QueryStakingParams() (*types.StakingParams, error) {
	// query btc checkpoint params
	ckptParamRes, err := bc.bbnClient.QueryClient.BTCCheckpointParams()
	if err != nil {
		return nil, fmt.Errorf("failed to query params of the btccheckpoint module: %v", err)
	}

	// query btc staking params
	stakingParamRes, err := bc.bbnClient.QueryClient.BTCStakingParams()
	if err != nil {
		return nil, fmt.Errorf("failed to query staking params: %v", err)
	}

	covenantPks := make([]*btcec.PublicKey, 0, len(stakingParamRes.Params.CovenantPks))
	for _, pk := range stakingParamRes.Params.CovenantPks {
		covPk, err := pk.ToBTCPK()
		if err != nil {
			return nil, fmt.Errorf("invalid covenant public key")
		}
		covenantPks = append(covenantPks, covPk)
	}
	slashingAddress, err := btcutil.DecodeAddress(stakingParamRes.Params.SlashingAddress, bc.btcParams)
	if err != nil {
		return nil, err
	}

	return &types.StakingParams{
		ComfirmationTimeBlocks:    ckptParamRes.Params.BtcConfirmationDepth,
		FinalizationTimeoutBlocks: ckptParamRes.Params.CheckpointFinalizationTimeout,
		MinSlashingTxFeeSat:       btcutil.Amount(stakingParamRes.Params.MinSlashingTxFeeSat),
		CovenantPks:               covenantPks,
		SlashingAddress:           slashingAddress,
		CovenantQuorum:            stakingParamRes.Params.CovenantQuorum,
		SlashingRate:              stakingParamRes.Params.SlashingRate,
		MinComissionRate:          stakingParamRes.Params.MinCommissionRate,
		MinUnbondingTime:          stakingParamRes.Params.MinUnbondingTime,
	}, nil
}

func (bc *BabylonController) reliablySendMsg(msg sdk.Msg) (*provider.RelayerTxResponse, error) {
	return bc.reliablySendMsgs([]sdk.Msg{msg})
}

func (bc *BabylonController) reliablySendMsgs(msgs []sdk.Msg) (*provider.RelayerTxResponse, error) {
	return bc.bbnClient.ReliablySendMsgs(
		context.Background(),
		msgs,
		expectedErrors,
		unrecoverableErrors,
	)
}

// SubmitCovenantSigs submits the Covenant signature via a MsgAddCovenantSig to Babylon if the daemon runs in Covenant mode
// it returns tx hash and error
func (bc *BabylonController) SubmitCovenantSigs(
	covPk *btcec.PublicKey,
	stakingTxHash string,
	slashingSigs [][]byte,
	unbondingSig *schnorr.Signature,
	unbondingSlashingSigs [][]byte,
) (*types.TxResponse, error) {
	bip340UnbondingSig := bbntypes.NewBIP340SignatureFromBTCSig(unbondingSig)

	msg := &btcstakingtypes.MsgAddCovenantSigs{
		Signer:                  bc.mustGetTxSigner(),
		Pk:                      bbntypes.NewBIP340PubKeyFromBTCPK(covPk),
		StakingTxHash:           stakingTxHash,
		SlashingTxSigs:          slashingSigs,
		UnbondingTxSig:          bip340UnbondingSig,
		SlashingUnbondingTxSigs: unbondingSlashingSigs,
	}

	res, err := bc.reliablySendMsg(msg)
	if err != nil {
		return nil, err
	}

	return &types.TxResponse{TxHash: res.TxHash, Events: res.Events}, nil
}

func (bc *BabylonController) QueryPendingDelegations(limit uint64) ([]*types.Delegation, error) {
	return bc.queryDelegationsWithStatus(btcstakingtypes.BTCDelegationStatus_PENDING, limit)
}

func (bc *BabylonController) QueryActiveDelegations(limit uint64) ([]*types.Delegation, error) {
	return bc.queryDelegationsWithStatus(btcstakingtypes.BTCDelegationStatus_ACTIVE, limit)
}

// queryDelegationsWithStatus queries BTC delegations that need a Covenant signature
// with the given status (either pending or unbonding)
// it is only used when the program is running in Covenant mode
func (bc *BabylonController) queryDelegationsWithStatus(status btcstakingtypes.BTCDelegationStatus, limit uint64) ([]*types.Delegation, error) {
	pagination := &sdkquery.PageRequest{
		Limit: limit,
	}

	res, err := bc.bbnClient.QueryClient.BTCDelegations(status, pagination)
	if err != nil {
		return nil, fmt.Errorf("failed to query BTC delegations: %v", err)
	}

	dels := make([]*types.Delegation, 0, len(res.BtcDelegations))
	for _, d := range res.BtcDelegations {
		dels = append(dels, ConvertDelegationType(d))
	}

	return dels, nil
}

func (bc *BabylonController) getNDelegations(
	fpBtcPk *bbntypes.BIP340PubKey,
	startKey []byte,
	n uint64,
) ([]*types.Delegation, []byte, error) {
	pagination := &sdkquery.PageRequest{
		Key:   startKey,
		Limit: n,
	}

	res, err := bc.bbnClient.QueryClient.FinalityProviderDelegations(fpBtcPk.MarshalHex(), pagination)

	if err != nil {
		return nil, nil, fmt.Errorf("failed to query BTC delegations: %v", err)
	}

	var delegations []*types.Delegation

	for _, dels := range res.BtcDelegatorDelegations {
		for _, d := range dels.Dels {
			delegations = append(delegations, ConvertDelegationType(d))
		}
	}

	var nextKey []byte

	if res.Pagination != nil && res.Pagination.NextKey != nil {
		nextKey = res.Pagination.NextKey
	}

	return delegations, nextKey, nil
}

func (bc *BabylonController) getNFinalityProviderDelegationsMatchingCriteria(
	fpBtcPk *bbntypes.BIP340PubKey,
	n uint64,
	match func(*types.Delegation) bool,
) ([]*types.Delegation, error) {
	batchSize := 100
	var delegations []*types.Delegation
	var startKey []byte

	for {
		dels, nextKey, err := bc.getNDelegations(fpBtcPk, startKey, uint64(batchSize))
		if err != nil {
			return nil, err
		}

		for _, del := range dels {
			if match(del) {
				delegations = append(delegations, del)
			}
		}

		if len(delegations) >= int(n) || len(nextKey) == 0 {
			break
		}

		startKey = nextKey
	}

	if len(delegations) > int(n) {
		// only return requested number of delegations
		return delegations[:n], nil
	} else {
		return delegations, nil
	}
}

func getContextWithCancel(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return ctx, cancel
}

func (bc *BabylonController) Close() error {
	if !bc.bbnClient.IsRunning() {
		return nil
	}

	return bc.bbnClient.Stop()
}

func ConvertDelegationType(del *btcstakingtypes.BTCDelegation) *types.Delegation {
	var (
		stakingTxHex  string
		slashingTxHex string
		covenantSigs  []*types.CovenantAdaptorSigInfo
		undelegation  *types.Undelegation
	)

	if del.StakingTx == nil {
		panic(fmt.Errorf("staking tx should not be empty in delegation"))
	}

	if del.SlashingTx == nil {
		panic(fmt.Errorf("slashing tx should not be empty in delegation"))
	}

	stakingTxHex = hex.EncodeToString(del.StakingTx)

	slashingTxHex = del.SlashingTx.ToHexStr()

	for _, s := range del.CovenantSigs {
		covSigInfo := &types.CovenantAdaptorSigInfo{
			Pk:   s.CovPk.MustToBTCPK(),
			Sigs: s.AdaptorSigs,
		}
		covenantSigs = append(covenantSigs, covSigInfo)
	}

	if del.BtcUndelegation != nil {
		undelegation = ConvertUndelegationType(del.BtcUndelegation)
	}

	fpBtcPks := make([]*btcec.PublicKey, 0, len(del.FpBtcPkList))
	for _, fp := range del.FpBtcPkList {
		fpBtcPks = append(fpBtcPks, fp.MustToBTCPK())
	}

	return &types.Delegation{
		BtcPk:           del.BtcPk.MustToBTCPK(),
		FpBtcPks:        fpBtcPks,
		TotalSat:        del.TotalSat,
		StartHeight:     del.StartHeight,
		EndHeight:       del.EndHeight,
		StakingTxHex:    stakingTxHex,
		SlashingTxHex:   slashingTxHex,
		CovenantSigs:    covenantSigs,
		UnbondingTime:   del.UnbondingTime,
		BtcUndelegation: undelegation,
	}
}

func ConvertUndelegationType(undel *btcstakingtypes.BTCUndelegation) *types.Undelegation {
	var (
		unbondingTxHex        string
		slashingTxHex         string
		covenantSlashingSigs  []*types.CovenantAdaptorSigInfo
		covenantUnbondingSigs []*types.CovenantSchnorrSigInfo
	)

	if undel.UnbondingTx == nil {
		panic(fmt.Errorf("staking tx should not be empty in undelegation"))
	}

	if undel.SlashingTx == nil {
		panic(fmt.Errorf("slashing tx should not be empty in undelegation"))
	}

	unbondingTxHex = hex.EncodeToString(undel.UnbondingTx)

	slashingTxHex = undel.SlashingTx.ToHexStr()

	for _, unbondingSig := range undel.CovenantUnbondingSigList {
		sig, err := unbondingSig.Sig.ToBTCSig()
		if err != nil {
			panic(err)
		}
		sigInfo := &types.CovenantSchnorrSigInfo{
			Pk:  unbondingSig.Pk.MustToBTCPK(),
			Sig: sig,
		}
		covenantUnbondingSigs = append(covenantUnbondingSigs, sigInfo)
	}

	for _, s := range undel.CovenantSlashingSigs {
		covSigInfo := &types.CovenantAdaptorSigInfo{
			Pk:   s.CovPk.MustToBTCPK(),
			Sigs: s.AdaptorSigs,
		}
		covenantSlashingSigs = append(covenantSlashingSigs, covSigInfo)
	}

	return &types.Undelegation{
		UnbondingTxHex:        unbondingTxHex,
		SlashingTxHex:         slashingTxHex,
		CovenantSlashingSigs:  covenantSlashingSigs,
		CovenantUnbondingSigs: covenantUnbondingSigs,
		DelegatorUnbondingSig: undel.DelegatorUnbondingSig,
	}
}

// Currently this is only used for e2e tests, probably does not need to add it into the interface
func (bc *BabylonController) CreateBTCDelegation(
	delBabylonPk *secp256k1.PubKey,
	delBtcPk *bbntypes.BIP340PubKey,
	fpPks []*btcec.PublicKey,
	pop *btcstakingtypes.ProofOfPossession,
	stakingTime uint32,
	stakingValue int64,
	stakingTxInfo *btcctypes.TransactionInfo,
	slashingTx *btcstakingtypes.BTCSlashingTx,
	delSlashingSig *bbntypes.BIP340Signature,
	unbondingTx []byte,
	unbondingTime uint32,
	unbondingValue int64,
	unbondingSlashingTx *btcstakingtypes.BTCSlashingTx,
	delUnbondingSlashingSig *bbntypes.BIP340Signature,
) (*types.TxResponse, error) {
	fpBtcPks := make([]bbntypes.BIP340PubKey, 0, len(fpPks))
	for _, v := range fpPks {
		fpBtcPks = append(fpBtcPks, *bbntypes.NewBIP340PubKeyFromBTCPK(v))
	}
	msg := &btcstakingtypes.MsgCreateBTCDelegation{
		Signer:                        bc.mustGetTxSigner(),
		BabylonPk:                     delBabylonPk,
		Pop:                           pop,
		BtcPk:                         delBtcPk,
		FpBtcPkList:                   fpBtcPks,
		StakingTime:                   stakingTime,
		StakingValue:                  stakingValue,
		StakingTx:                     stakingTxInfo,
		SlashingTx:                    slashingTx,
		DelegatorSlashingSig:          delSlashingSig,
		UnbondingTx:                   unbondingTx,
		UnbondingTime:                 unbondingTime,
		UnbondingValue:                unbondingValue,
		UnbondingSlashingTx:           unbondingSlashingTx,
		DelegatorUnbondingSlashingSig: delUnbondingSlashingSig,
	}

	res, err := bc.reliablySendMsg(msg)
	if err != nil {
		return nil, err
	}

	return &types.TxResponse{TxHash: res.TxHash}, nil
}

func (bc *BabylonController) RegisterFinalityProvider(
	bbnPubKey *secp256k1.PubKey, btcPubKey *bbntypes.BIP340PubKey, commission *sdkmath.LegacyDec,
	description *stakingtypes.Description, pop *btcstakingtypes.ProofOfPossession) (*provider.RelayerTxResponse, error) {
	registerMsg := &btcstakingtypes.MsgCreateFinalityProvider{
		Signer:      bc.mustGetTxSigner(),
		Commission:  commission,
		BabylonPk:   bbnPubKey,
		BtcPk:       btcPubKey,
		Description: description,
		Pop:         pop,
	}

	return bc.reliablySendMsgs([]sdk.Msg{registerMsg})
}

// Insert BTC block header using rpc client
// Currently this is only used for e2e tests, probably does not need to add it into the interface
func (bc *BabylonController) InsertBtcBlockHeaders(headers []bbntypes.BTCHeaderBytes) (*provider.RelayerTxResponse, error) {
	msg := &btclctypes.MsgInsertHeaders{
		Signer:  bc.mustGetTxSigner(),
		Headers: headers,
	}

	res, err := bc.reliablySendMsg(msg)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// QueryFinalityProvider queries finality providers
// Currently this is only used for e2e tests, probably does not need to add this into the interface
func (bc *BabylonController) QueryFinalityProviders() ([]*btcstakingtypes.FinalityProvider, error) {
	var fps []*btcstakingtypes.FinalityProvider
	pagination := &sdkquery.PageRequest{
		Limit: 100,
	}

	ctx, cancel := getContextWithCancel(bc.cfg.Timeout)
	defer cancel()

	clientCtx := sdkclient.Context{Client: bc.bbnClient.RPCClient}

	queryClient := btcstakingtypes.NewQueryClient(clientCtx)

	for {
		queryRequest := &btcstakingtypes.QueryFinalityProvidersRequest{
			Pagination: pagination,
		}
		res, err := queryClient.FinalityProviders(ctx, queryRequest)
		if err != nil {
			return nil, fmt.Errorf("failed to query finality providers: %v", err)
		}
		fps = append(fps, res.FinalityProviders...)
		if res.Pagination == nil || res.Pagination.NextKey == nil {
			break
		}

		pagination.Key = res.Pagination.NextKey
	}

	return fps, nil
}

// Currently this is only used for e2e tests, probably does not need to add this into the interface
func (bc *BabylonController) QueryBtcLightClientTip() (*btclctypes.BTCHeaderInfo, error) {
	ctx, cancel := getContextWithCancel(bc.cfg.Timeout)
	defer cancel()

	clientCtx := sdkclient.Context{Client: bc.bbnClient.RPCClient}

	queryClient := btclctypes.NewQueryClient(clientCtx)

	queryRequest := &btclctypes.QueryTipRequest{}
	res, err := queryClient.Tip(ctx, queryRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to query BTC tip: %v", err)
	}

	return res.Header, nil
}

// Currently this is only used for e2e tests, probably does not need to add this into the interface
func (bc *BabylonController) QueryFinalityProviderDelegations(fpBtcPk *bbntypes.BIP340PubKey, max uint64) ([]*types.Delegation, error) {
	return bc.getNFinalityProviderDelegationsMatchingCriteria(
		fpBtcPk,
		max,
		// fitlering function which always returns true as we want all delegations
		func(*types.Delegation) bool { return true },
	)
}

// Currently this is only used for e2e tests, probably does not need to add this into the interface
func (bc *BabylonController) QueryVotesAtHeight(height uint64) ([]bbntypes.BIP340PubKey, error) {
	ctx, cancel := getContextWithCancel(bc.cfg.Timeout)
	defer cancel()

	clientCtx := sdkclient.Context{Client: bc.bbnClient.RPCClient}

	queryClient := finalitytypes.NewQueryClient(clientCtx)

	// query all the unsigned delegations
	queryRequest := &finalitytypes.QueryVotesAtHeightRequest{
		Height: height,
	}
	res, err := queryClient.VotesAtHeight(ctx, queryRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to query BTC delegations: %w", err)
	}

	return res.BtcPks, nil
}
