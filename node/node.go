package node

import (
	"context"
	"sync"
	"time"

	"errors"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	nodetypes "github.com/initia-labs/opinit-bots-go/node/types"
	"github.com/initia-labs/opinit-bots-go/types"
	"go.uber.org/zap"
)

type Node struct {
	*rpchttp.HTTP

	cfg    nodetypes.NodeConfig
	db     types.DB
	logger *zap.Logger

	eventHandlers     map[string]nodetypes.EventHandlerFn
	txHandler         nodetypes.TxHandlerFn
	beginBlockHandler nodetypes.BeginBlockHandlerFn
	endBlockHandler   nodetypes.EndBlockHandlerFn

	cdc        codec.Codec
	txConfig   client.TxConfig
	keyBase    keyring.Keyring
	keyAddress sdk.AccAddress
	txf        tx.Factory

	lastProcessedBlockHeight uint64

	// local pending txs, which is following Queue data structure
	pendingTxMu *sync.Mutex
	pendingTxs  []nodetypes.PendingTxInfo

	pendingProcessedMsgs []nodetypes.ProcessedMsgs

	txChannel chan nodetypes.ProcessedMsgs
}

func NewNode(cfg nodetypes.NodeConfig, db types.DB, logger *zap.Logger, cdc codec.Codec, txConfig client.TxConfig) (*Node, error) {
	client, err := client.NewClientFromNode(cfg.RPC)
	if err != nil {
		return nil, err
	}

	// Use memory keyring for now
	// TODO: may use os keyring later
	keyBase, err := keyring.New(cfg.ChainID, "memory", "", nil, cdc)
	if err != nil {
		return nil, err
	}

	n := &Node{
		HTTP: client,

		cfg:    cfg,
		db:     db,
		logger: logger,

		eventHandlers: make(map[string]nodetypes.EventHandlerFn),

		cdc:      cdc,
		txConfig: txConfig,
		keyBase:  keyBase,

		pendingTxMu: &sync.Mutex{},
		pendingTxs:  make([]nodetypes.PendingTxInfo, 0),

		pendingProcessedMsgs: make([]nodetypes.ProcessedMsgs, 0),

		txChannel: make(chan nodetypes.ProcessedMsgs),
	}

	err = n.loadSyncInfo()
	if err != nil {
		return nil, err
	}

	status, err := n.Status(context.Background())
	if err != nil {
		return nil, err
	}
	if status.SyncInfo.CatchingUp {
		return nil, errors.New("node is catching up")
	}

	if n.HasKey() {
		err := n.prepareBroadcaster(uint64(status.SyncInfo.LatestBlockHeight), status.SyncInfo.LatestBlockTime)
		if err != nil {
			return nil, err
		}
	}

	return n, nil
}

func (n Node) Start(ctx context.Context, errCh chan error) {
	go func() {
		err := n.txBroadcastLooper(ctx)
		if err != nil {
			errCh <- err
		}
	}()

	// broadcast pending msgs first before executing block process looper
	// @dev: these pending processed data is filled at initialization(`NewNode`).
	for _, processedMsg := range n.pendingProcessedMsgs {
		n.BroadcastMsgs(processedMsg)
	}

	go func() {
		err := n.blockProcessLooper(ctx)
		if err != nil {
			errCh <- err
		}
	}()
}

func (n Node) HasKey() bool {
	return n.cfg.Mnemonic != ""
}

func (n *Node) prepareBroadcaster(_ /*lastBlockHeight*/ uint64, lastBlockTime time.Time) error {
	_, err := n.keyBase.NewAccount(nodetypes.KEY_NAME, n.cfg.Mnemonic, "", hd.CreateHDPath(sdk.GetConfig().GetCoinType(), 0, 0).String(), hd.Secp256k1)
	if err != nil {
		return err
	}
	// to check if the key is normally created
	// TODO: delete this code
	key, err := n.keyBase.Key(nodetypes.KEY_NAME)
	if err != nil {
		return err
	}

	addr, err := key.GetAddress()
	if err != nil {
		return err
	}
	n.keyAddress = addr

	n.txf = tx.Factory{}.
		WithAccountRetriever(n).
		WithChainID(n.cfg.ChainID).
		WithTxConfig(n.txConfig).
		WithGasAdjustment(nodetypes.GAS_ADJUSTMENT).
		WithGasPrices(n.cfg.GasPrice).
		WithKeybase(n.keyBase).
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)

	err = n.loadAccount()
	if err != nil {
		return err
	}

	dbBatchKVs := make([]types.RawKV, 0)

	loadedPendingTxs, err := n.loadPendingTxs()
	if err != nil {
		return err
	}
	// TODO: handle mismatched sequence & pending txs
	if len(loadedPendingTxs) > 0 {
		pendingTxTime := time.Unix(0, loadedPendingTxs[0].Timestamp)

		// if we have pending txs, wait until timeout
		if timeoutTime := pendingTxTime.Add(nodetypes.TX_TIMEOUT); lastBlockTime.Before(timeoutTime) {
			timer := time.NewTimer(timeoutTime.Sub(lastBlockTime))
			<-timer.C
		}

		// convert pending txs to raw kv pairs for deletion
		pendingKVs, err := n.PendingTxsToRawKV(loadedPendingTxs, true)
		if err != nil {
			return err
		}

		// add pending txs delegation to db batch
		dbBatchKVs = append(dbBatchKVs, pendingKVs...)

		// convert pending txs to pending msgs
		for i, txInfo := range loadedPendingTxs {
			tx, err := n.DecodeTx(txInfo.Tx)
			if err != nil {
				return err
			}
			if txInfo.Save {
				n.pendingProcessedMsgs = append(n.pendingProcessedMsgs, nodetypes.ProcessedMsgs{
					Msgs:      tx.GetMsgs(),
					Timestamp: time.Now().UnixNano(),
					Save:      txInfo.Save,
				})
			}

			n.logger.Debug("pending tx", zap.Int("index", i), zap.String("tx", txInfo.String()))
		}
	}

	loadedProcessedMsgs, err := n.loadProcessedMsgs()
	if err != nil {
		return err
	}

	// @sh-cha: in L233, we are saving processed data to db with updated timestamp
	//          seems we can remove this part.
	//
	// kvProcessedMsgs, err := n.ProcessedMsgsToRawKV(loadedProcessedMsgs, true)
	// if err != nil {
	// 	return err
	// }
	// dbBatchKVs = append(dbBatchKVs, kvProcessedMsgs...)

	for i, pendingMsgs := range loadedProcessedMsgs {
		loadedProcessedMsgs[i].Timestamp = time.Now().UnixNano()
		n.logger.Debug("pending msgs", zap.Int("index", i), zap.String("msgs", pendingMsgs.String()))
	}

	n.pendingProcessedMsgs = append(n.pendingProcessedMsgs, loadedProcessedMsgs...)
	kvProcessedMsgs, err := n.ProcessedMsgsToRawKV(n.pendingProcessedMsgs, false)
	if err != nil {
		return err
	}
	dbBatchKVs = append(dbBatchKVs, kvProcessedMsgs...)

	// save all pending msgs first, then broadcast them
	err = n.db.RawBatchSet(dbBatchKVs...)
	if err != nil {
		return err
	}

	return nil
}

func (n Node) GetHeight() uint64 {
	return n.lastProcessedBlockHeight + 1
}

func (n *Node) getClientCtx() client.Context {
	return client.Context{}.WithClient(n).
		WithInterfaceRegistry(n.cdc.InterfaceRegistry()).
		WithChainID(n.cfg.ChainID).
		WithCodec(n.cdc).
		WithFromAddress(n.keyAddress)
}

func (n *Node) RegisterTxHandler(fn nodetypes.TxHandlerFn) {
	n.txHandler = fn
}

func (n *Node) RegisterEventHandler(eventType string, fn nodetypes.EventHandlerFn) {
	n.eventHandlers[eventType] = fn
}

func (n *Node) RegisterBeginBlockHandler(fn nodetypes.BeginBlockHandlerFn) {
	n.beginBlockHandler = fn
}

func (n *Node) RegisterEndBlockHandler(fn nodetypes.EndBlockHandlerFn) {
	n.endBlockHandler = fn
}
