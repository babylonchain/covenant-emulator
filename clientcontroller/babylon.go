package clientcontroller

import (
	"context"
	"fmt"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/btcsuite/btcd/btcec/v2"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	bbnclient "github.com/babylonchain/babylon/client/client"
	bbntypes "github.com/babylonchain/babylon/types"
	btcctypes "github.com/babylonchain/babylon/x/btccheckpoint/types"
	btclctypes "github.com/babylonchain/babylon/x/btclightclient/types"
	btcstakingtypes "github.com/babylonchain/babylon/x/btcstaking/types"
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

func (bc *BabylonController) QueryStakingParamsByVersion(version uint32) (*types.StakingParams, error) {
	// query btc checkpoint params
	ckptParamRes, err := bc.bbnClient.QueryClient.BTCCheckpointParams()
	if err != nil {
		return nil, fmt.Errorf("failed to query params of the btccheckpoint module: %v", err)
	}

	// query btc staking params
	stakingParamRes, err := bc.bbnClient.QueryClient.BTCStakingParamsByVersion(version)
	if err != nil {
		return nil, fmt.Errorf("failed to query staking params with version %d: %v", version, err)
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
func (bc *BabylonController) SubmitCovenantSigs(covSigs []*types.CovenantSigs) (*types.TxResponse, error) {
	msgs := make([]sdk.Msg, 0, len(covSigs))
	for _, covSig := range covSigs {
		bip340UnbondingSig := bbntypes.NewBIP340SignatureFromBTCSig(covSig.UnbondingSig)
		msgs = append(msgs, &btcstakingtypes.MsgAddCovenantSigs{
			Signer:                  bc.mustGetTxSigner(),
			Pk:                      bbntypes.NewBIP340PubKeyFromBTCPK(covSig.PublicKey),
			StakingTxHash:           covSig.StakingTxHash.String(),
			SlashingTxSigs:          covSig.SlashingSigs,
			UnbondingTxSig:          bip340UnbondingSig,
			SlashingUnbondingTxSigs: covSig.SlashingUnbondingSigs,
		})
	}
	res, err := bc.reliablySendMsgs(msgs)
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
	for _, delResp := range res.BtcDelegations {
		del, err := DelegationRespToDelegation(delResp)
		if err != nil {
			return nil, err
		}

		dels = append(dels, del)
	}

	return dels, nil
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

func DelegationRespToDelegation(del *btcstakingtypes.BTCDelegationResponse) (*types.Delegation, error) {
	var (
		covenantSigs []*types.CovenantAdaptorSigInfo
		undelegation *types.Undelegation
		err          error
	)

	if del.StakingTxHex == "" {
		return nil, fmt.Errorf("staking tx should not be empty in delegation")
	}

	if del.SlashingTxHex == "" {
		return nil, fmt.Errorf("slashing tx should not be empty in delegation")
	}

	for _, s := range del.CovenantSigs {
		covSigInfo := &types.CovenantAdaptorSigInfo{
			Pk:   s.CovPk.MustToBTCPK(),
			Sigs: s.AdaptorSigs,
		}
		covenantSigs = append(covenantSigs, covSigInfo)
	}

	if del.UndelegationResponse != nil {
		undelegation, err = UndelegationRespToUndelegation(del.UndelegationResponse)
		if err != nil {
			return nil, err
		}
	}

	fpBtcPks := make([]*btcec.PublicKey, 0, len(del.FpBtcPkList))
	for _, fp := range del.FpBtcPkList {
		fpBtcPks = append(fpBtcPks, fp.MustToBTCPK())
	}

	return &types.Delegation{
		BtcPk:            del.BtcPk.MustToBTCPK(),
		FpBtcPks:         fpBtcPks,
		TotalSat:         del.TotalSat,
		StartHeight:      del.StartHeight,
		EndHeight:        del.EndHeight,
		StakingTxHex:     del.StakingTxHex,
		SlashingTxHex:    del.SlashingTxHex,
		StakingOutputIdx: del.StakingOutputIdx,
		CovenantSigs:     covenantSigs,
		UnbondingTime:    del.UnbondingTime,
		BtcUndelegation:  undelegation,
	}, nil
}

func UndelegationRespToUndelegation(undel *btcstakingtypes.BTCUndelegationResponse) (*types.Undelegation, error) {
	var (
		covenantSlashingSigs  []*types.CovenantAdaptorSigInfo
		covenantUnbondingSigs []*types.CovenantSchnorrSigInfo
		err                   error
	)

	if undel.UnbondingTxHex == "" {
		return nil, fmt.Errorf("staking tx should not be empty in undelegation")
	}

	if undel.SlashingTxHex == "" {
		return nil, fmt.Errorf("slashing tx should not be empty in undelegation")
	}

	for _, unbondingSig := range undel.CovenantUnbondingSigList {
		sig, err := unbondingSig.Sig.ToBTCSig()
		if err != nil {
			return nil, err
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

	delegatorUnbondingSig := new(bbntypes.BIP340Signature)
	if undel.DelegatorUnbondingSigHex != "" {
		delegatorUnbondingSig, err = bbntypes.NewBIP340SignatureFromHex(undel.DelegatorUnbondingSigHex)
		if err != nil {
			return nil, err
		}
	}

	return &types.Undelegation{
		UnbondingTxHex:        undel.UnbondingTxHex,
		SlashingTxHex:         undel.SlashingTxHex,
		CovenantSlashingSigs:  covenantSlashingSigs,
		CovenantUnbondingSigs: covenantUnbondingSigs,
		DelegatorUnbondingSig: delegatorUnbondingSig,
	}, nil
}

// Currently this is only used for e2e tests, probably does not need to add it into the interface
func (bc *BabylonController) CreateBTCDelegation(
	delBtcPk *bbntypes.BIP340PubKey,
	fpPks []*btcec.PublicKey,
	pop *btcstakingtypes.ProofOfPossessionBTC,
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
		StakerAddr:                    bc.mustGetTxSigner(),
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

// Register a finality provider to Babylon
// Currently this is only used for e2e tests, probably does not need to add it into the interface
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
func (bc *BabylonController) QueryFinalityProviders() ([]*btcstakingtypes.FinalityProviderResponse, error) {
	var fps []*btcstakingtypes.FinalityProviderResponse
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
func (bc *BabylonController) QueryBtcLightClientTip() (*btclctypes.BTCHeaderInfoResponse, error) {
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
