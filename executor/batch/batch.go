package batch

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	opchildtypes "github.com/initia-labs/OPinit/x/opchild/types"
	ophosttypes "github.com/initia-labs/OPinit/x/ophost/types"
	"github.com/initia-labs/opinit-bots-go/executor/child"
	executortypes "github.com/initia-labs/opinit-bots-go/executor/types"
	nodetypes "github.com/initia-labs/opinit-bots-go/node/types"
	"github.com/initia-labs/opinit-bots-go/types"
	"go.uber.org/zap"

	"github.com/initia-labs/opinit-bots-go/node"

	dbtypes "github.com/initia-labs/opinit-bots-go/db/types"
)

type hostNode interface {
	QueryBatchInfos() (*ophosttypes.QueryBatchInfosResponse, error)
}

type compressionFunc interface {
	Write([]byte) (int, error)
	Reset(io.Writer)
	Close() error
}

var SubmissionKey = []byte("submission_time")

type BatchSubmitter struct {
	version uint8

	node *node.Node
	host hostNode
	da   executortypes.DANode

	bridgeInfo opchildtypes.BridgeInfo

	cfg      nodetypes.NodeConfig
	batchCfg executortypes.BatchConfig
	db       types.DB
	logger   *zap.Logger

	opchildQueryClient opchildtypes.QueryClient

	batchInfoMu *sync.Mutex
	batchInfos  []ophosttypes.BatchInfoWithOutput
	batchWriter compressionFunc
	batchFile   *os.File
	batchHeader *executortypes.BatchHeader

	processedMsgs []nodetypes.ProcessedMsgs
	homePath      string

	lastSubmissionTime time.Time
}

func NewBatchSubmitter(version uint8, cfg nodetypes.NodeConfig, batchCfg executortypes.BatchConfig, db types.DB, logger *zap.Logger, homePath string) *BatchSubmitter {
	cfg.Account = ""
	appCodec, txConfig, bech32Prefix, err := child.GetCodec(cfg.ChainID)
	if err != nil {
		panic(err)
	}
	node, err := node.NewNode(nodetypes.PROCESS_TYPE_RAW, cfg, db, logger, appCodec, txConfig, homePath, bech32Prefix, "", nil)
	if err != nil {
		panic(err)
	}

	ch := &BatchSubmitter{
		version: version,

		node: node,

		cfg:      cfg,
		batchCfg: batchCfg,

		db:     db,
		logger: logger,

		opchildQueryClient: opchildtypes.NewQueryClient(node),

		batchInfoMu: &sync.Mutex{},

		processedMsgs: make([]nodetypes.ProcessedMsgs, 0),
		homePath:      homePath,
	}
	return ch
}

func (bs *BatchSubmitter) Initialize(host hostNode, bridgeInfo opchildtypes.BridgeInfo) error {
	bs.host = host
	bs.bridgeInfo = bridgeInfo

	res, err := bs.host.QueryBatchInfos()
	if err != nil {
		return err
	}
	bs.batchInfos = res.BatchInfos
	if len(bs.batchInfos) == 0 {
		return errors.New("no batch info")
	}
	for _, batchInfo := range bs.batchInfos {
		if len(bs.batchInfos) == 1 || (batchInfo.Output.L2BlockNumber+1) >= bs.node.GetHeight() {
			break
		}
		bs.DequeueBatchInfo()
	}

	bs.batchFile, err = os.OpenFile(bs.homePath+"/batch", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		return err
	}

	err = bs.LoadSubmissionInfo()
	if err != nil {
		return err
	}

	bs.node.RegisterRawBlockHandler(bs.rawBlockHandler)
	return nil
}

func (bs *BatchSubmitter) SetDANode(da executortypes.DANode) error {
	bs.da = da
	if !bs.da.HasKey() {
		return errors.New("da has no key")
	}
	return nil
}

func (bs *BatchSubmitter) Start(ctx context.Context) {
	bs.logger.Info("batch start", zap.Uint64("height", bs.node.GetHeight()))
	bs.node.Start(ctx)
}

func (bs *BatchSubmitter) SetBridgeInfo(bridgeInfo opchildtypes.BridgeInfo) {
	bs.bridgeInfo = bridgeInfo
}

func (bs *BatchSubmitter) LoadSubmissionInfo() error {
	val, err := bs.db.Get(SubmissionKey)
	if err != nil {
		if err == dbtypes.ErrNotFound {
			return nil
		}
		return err
	}
	bs.lastSubmissionTime = time.Unix(0, dbtypes.ToInt64(val))
	return nil
}

func (bs *BatchSubmitter) SubmissionInfoToRawKV(timestamp int64) types.RawKV {
	return types.RawKV{
		Key:   bs.db.PrefixedKey(SubmissionKey),
		Value: dbtypes.FromInt64(timestamp),
	}
}

func (bs *BatchSubmitter) ChainID() string {
	return bs.cfg.ChainID
}

func (bs *BatchSubmitter) DA() executortypes.DANode {
	return bs.da
}
