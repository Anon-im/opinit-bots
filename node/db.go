package node

import (
	dbtypes "github.com/initia-labs/opinit-bots-go/db/types"
	nodetypes "github.com/initia-labs/opinit-bots-go/node/types"
	"github.com/initia-labs/opinit-bots-go/types"
	"go.uber.org/zap"
)

<<<<<<< HEAD
// should use safely
func (n *Node) SetSyncInfo(height uint64) {
	n.lastProcessedBlockHeight = height
}

func (n *Node) SaveSyncInfo(height uint64) error {
	return n.db.Set(nodetypes.LastProcessedBlockHeightKey, dbtypes.FromUint64(height))
||||||| 222d087
func (n *Node) SaveSyncInfo() error {
	return n.db.Set(nodetypes.LastProcessedBlockHeightKey, dbtypes.FromUint64(n.lastProcessedBlockHeight))
=======
//////////////
// SyncInfo //
//////////////

func (n *Node) SaveSyncInfo() error {
	return n.db.Set(nodetypes.LastProcessedBlockHeightKey, dbtypes.FromUint64(n.lastProcessedBlockHeight))
>>>>>>> feat/executor
}

func (n *Node) SyncInfoToRawKV(height uint64) types.RawKV {
	return types.RawKV{
		Key:   n.db.PrefixedKey(nodetypes.LastProcessedBlockHeightKey),
		Value: dbtypes.FromUint64(height),
	}
}

func (n *Node) loadSyncInfo() error {
	data, err := n.db.Get(nodetypes.LastProcessedBlockHeightKey)
	if err == dbtypes.ErrNotFound {
		return nil
	} else if err != nil {
		return err
	}

	n.lastProcessedBlockHeight, err = dbtypes.ToUint64(data)
	if err != nil {
		return err
	}

	n.logger.Info("load sync info", zap.Uint64("last_processed_height", n.lastProcessedBlockHeight))
	return nil
}

///////////////
// PendingTx //
///////////////

func (n Node) savePendingTx(sequence uint64, txInfo nodetypes.PendingTxInfo) error {
	data, err := txInfo.Marshal()
	if err != nil {
		return err
	}
	return n.db.Set(nodetypes.PrefixedPendingTx(sequence), data)
}

func (n Node) deletePendingTx(sequence uint64) error {
	return n.db.Delete(nodetypes.PrefixedPendingTx(sequence))
}

func (n *Node) loadPendingTxs() (txs []nodetypes.PendingTxInfo, err error) {
	iterErr := n.db.PrefixedIterate(nodetypes.PendingTxsKey, func(_, value []byte) (stop bool, err error) {
		txInfo := nodetypes.PendingTxInfo{}
		err = txInfo.Unmarshal(value)
		if err != nil {
			return true, err
		}
		txs = append(txs, txInfo)
		return false, nil
	})
	if iterErr != nil {
		return nil, iterErr
	}

	n.logger.Info("load pending txs", zap.Int("count", len(txs)))
	return txs, err
}

// PendingTxsToRawKV converts pending txs to raw kv pairs.
// If delete is true, it will return kv pairs for deletion (empty value).
func (n *Node) PendingTxsToRawKV(txInfos []nodetypes.PendingTxInfo, delete bool) ([]types.RawKV, error) {
	kvs := make([]types.RawKV, 0, len(txInfos))
	for _, txInfo := range txInfos {
		var data []byte
		var err error

		if !delete && txInfo.Save {
			data, err = txInfo.Marshal()
			if err != nil {
				return nil, err
			}
		}
		kvs = append(kvs, types.RawKV{
			Key:   n.db.PrefixedKey(nodetypes.PrefixedPendingTx(txInfo.Sequence)),
			Value: data,
		})
	}
	return kvs, nil
}

<<<<<<< HEAD
func (n *Node) RawKVProcessedData(processedData []nodetypes.ProcessedMsgs, delete bool) ([]types.KV, error) {
	kvs := make([]types.KV, 0, len(processedData))
	for _, processedMsgs := range processedData {
||||||| 222d087
func (n *Node) RawKVProcessedData(processedData []nodetypes.ProcessedMsgs, delete bool) ([]types.KV, error) {
	kvs := make([]types.KV, 0, len(processedData))
	for _, processedMsgs := range processedData {
		if !processedMsgs.Save {
			continue
		}

=======
///////////////////
// ProcessedMsgs //
///////////////////

// ProcessedMsgsToRawKV converts processed data to raw kv pairs.
// If delete is true, it will return kv pairs for deletion (empty value).
func (n *Node) ProcessedMsgsToRawKV(ProcessedMsgs []nodetypes.ProcessedMsgs, delete bool) ([]types.RawKV, error) {
	kvs := make([]types.RawKV, 0, len(ProcessedMsgs))
	for _, processedMsgs := range ProcessedMsgs {
		if !processedMsgs.Save {
			continue
		}

>>>>>>> feat/executor
		var data []byte
		var err error

<<<<<<< HEAD
		if !delete && processedMsgs.Save {
			data, err = processedMsgs.Marshal()
||||||| 222d087
		if !delete {
			data, err = processedMsgs.Marshal()
=======
		if !delete {
			data, err = processedMsgs.MarshalInterfaceJSON(n.cdc)
>>>>>>> feat/executor
			if err != nil {
				return nil, err
			}
		}
		kvs = append(kvs, types.RawKV{
			Key:   n.db.PrefixedKey(nodetypes.PrefixedProcessedMsgs(uint64(processedMsgs.Timestamp))),
			Value: data,
		})
	}
	return kvs, nil
}

// currently no use case, but keep it for future use
// func (n *Node) saveProcessedMsgs(processedMsgs nodetypes.ProcessedMsgs) error {
// 	data, err := processedMsgs.Marshal()
// 	if err != nil {
// 		return err
// 	}
// 	return n.db.Set(nodetypes.PrefixedProcessedMsgs(uint64(processedMsgs.Timestamp)), data)
// }

func (n *Node) loadProcessedMsgs() (ProcessedMsgs []nodetypes.ProcessedMsgs, err error) {
	iterErr := n.db.PrefixedIterate(nodetypes.ProcessedMsgsKey, func(_, value []byte) (stop bool, err error) {
		var processedMsgs nodetypes.ProcessedMsgs
		err = processedMsgs.UnmarshalInterfaceJSON(n.cdc, value)
		if err != nil {
			return true, err
		}
		ProcessedMsgs = append(ProcessedMsgs, processedMsgs)
		return false, nil
	})

	if iterErr != nil {
		return nil, iterErr
	}
	n.logger.Info("load pending processed msgs", zap.Int("count", len(ProcessedMsgs)))
	return ProcessedMsgs, nil
}

func (n *Node) deleteProcessedMsgs(timestamp int64) error {
	return n.db.Delete(nodetypes.PrefixedProcessedMsgs(uint64(timestamp)))
}
