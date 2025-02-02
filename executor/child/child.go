package child

import (
	"context"
	"time"

	"go.uber.org/zap"

	sdk "github.com/cosmos/cosmos-sdk/types"

	opchildtypes "github.com/initia-labs/OPinit/x/opchild/types"
	ophosttypes "github.com/initia-labs/OPinit/x/ophost/types"

	btypes "github.com/initia-labs/opinit-bots/node/broadcaster/types"
	nodetypes "github.com/initia-labs/opinit-bots/node/types"
	"github.com/initia-labs/opinit-bots/types"

	childprovider "github.com/initia-labs/opinit-bots/provider/child"
)

type hostNode interface {
	HasKey() bool
	BaseAccountAddressString() (string, error)
	BroadcastMsgs(btypes.ProcessedMsgs)
	ProcessedMsgsToRawKV([]btypes.ProcessedMsgs, bool) ([]types.RawKV, error)
	QueryLastOutput(context.Context, uint64) (*ophosttypes.QueryOutputProposalResponse, error)
	QueryOutput(context.Context, uint64, uint64, int64) (*ophosttypes.QueryOutputProposalResponse, error)

	GetMsgProposeOutput(uint64, uint64, int64, []byte) (sdk.Msg, string, error)
}

type Child struct {
	*childprovider.BaseChild

	host hostNode

	nextOutputTime        time.Time
	finalizingBlockHeight int64

	// status info
	lastUpdatedOracleL1Height         int64
	lastFinalizedDepositL1BlockHeight int64
	lastFinalizedDepositL1Sequence    uint64
	lastOutputTime                    time.Time

	batchKVs        []types.RawKV
	addressIndexMap map[string]uint64
}

func NewChildV1(
	cfg nodetypes.NodeConfig,
	db types.DB, logger *zap.Logger,
) *Child {
	return &Child{
		BaseChild:       childprovider.NewBaseChildV1(cfg, db, logger),
		batchKVs:        make([]types.RawKV, 0),
		addressIndexMap: make(map[string]uint64),
	}
}

func (ch *Child) Initialize(
	ctx context.Context,
	processedHeight int64,
	startOutputIndex uint64,
	host hostNode,
	bridgeInfo ophosttypes.QueryBridgeResponse,
	keyringConfig *btypes.KeyringConfig,
	oracleKeyringConfig *btypes.KeyringConfig,
	disableDeleteFutureWithdrawals bool,
) error {
	l2Sequence, err := ch.BaseChild.Initialize(
		ctx,
		processedHeight,
		startOutputIndex,
		bridgeInfo,
		keyringConfig,
		oracleKeyringConfig,
		disableDeleteFutureWithdrawals,
	)
	if err != nil {
		return err
	}
	if l2Sequence != 0 {
		err = ch.DeleteFutureWithdrawals(l2Sequence)
		if err != nil {
			return err
		}
	}

	ch.host = host
	ch.registerHandlers()
	return nil
}

func (ch *Child) registerHandlers() {
	ch.Node().RegisterBeginBlockHandler(ch.beginBlockHandler)
	ch.Node().RegisterEventHandler(opchildtypes.EventTypeFinalizeTokenDeposit, ch.finalizeDepositHandler)
	ch.Node().RegisterEventHandler(opchildtypes.EventTypeUpdateOracle, ch.updateOracleHandler)
	ch.Node().RegisterEventHandler(opchildtypes.EventTypeInitiateTokenWithdrawal, ch.initiateWithdrawalHandler)
	ch.Node().RegisterEndBlockHandler(ch.endBlockHandler)
}
