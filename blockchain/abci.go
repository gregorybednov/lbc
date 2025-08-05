package blockchain

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dgraph-io/badger"
	abci "github.com/tendermint/tendermint/abci/types"
)

type PromiseApp struct {
	db           *badger.DB
	currentBatch *badger.Txn
}

func NewPromiseApp(db *badger.DB) *PromiseApp {
	return &PromiseApp{db: db}
}

func (app *PromiseApp) BeginBlock(_ abci.RequestBeginBlock) abci.ResponseBeginBlock {
	if app.currentBatch != nil {
		app.currentBatch.Discard()
	}
	app.currentBatch = app.db.NewTransaction(true)
	return abci.ResponseBeginBlock{}
}

func (app *PromiseApp) DeliverTx(req abci.RequestDeliverTx) abci.ResponseDeliverTx {
	var tx map[string]interface{}
	if err := json.Unmarshal(req.Tx, &tx); err != nil {
		return abci.ResponseDeliverTx{Code: 1, Log: "invalid JSON"}
	}

	id, ok := tx["id"].(string)
	if !ok || id == "" {
		return abci.ResponseDeliverTx{Code: 2, Log: "missing id"}
	}

	data, _ := json.Marshal(tx)

	if app.currentBatch == nil {
		app.currentBatch = app.db.NewTransaction(true)
	}
	err := app.currentBatch.Set([]byte(id), data)
	if err != nil {
		return abci.ResponseDeliverTx{Code: 1, Log: fmt.Sprintf("failed to set: %v", err)}
	}

	return abci.ResponseDeliverTx{Code: 0}
}

func (app *PromiseApp) CheckTx(req abci.RequestCheckTx) abci.ResponseCheckTx {
	var tx map[string]interface{}
	if err := json.Unmarshal(req.Tx, &tx); err != nil {
		return abci.ResponseCheckTx{Code: 1, Log: "invalid JSON"}
	}
	id, ok := tx["id"].(string)
	if !ok || id == "" {
		return abci.ResponseCheckTx{Code: 2, Log: "missing id"}
	}

	// проверка уникальности id
	err := app.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(id))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		return fmt.Errorf("id exists")
	})
	if err != nil {
		return abci.ResponseCheckTx{Code: 3, Log: "duplicate id"}
	}

	return abci.ResponseCheckTx{Code: 0}
}

func (app *PromiseApp) Commit() abci.ResponseCommit {
	if app.currentBatch != nil {
		err := app.currentBatch.Commit()
		if err != nil {
			fmt.Printf("Commit error: %v\n", err)
		}
		app.currentBatch = nil
	}
	return abci.ResponseCommit{Data: []byte{}}
}

// SELECT * FROM <table>
func (app *PromiseApp) Query(req abci.RequestQuery) abci.ResponseQuery {
	parts := strings.Split(strings.Trim(req.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "list" {
		return abci.ResponseQuery{Code: 1, Log: "unsupported query"}
	}
	prefix := []byte(parts[1] + ":")

	var result []json.RawMessage
	err := app.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(v []byte) error {
				var raw json.RawMessage = make([]byte, len(v))
				copy(raw, v)
				result = append(result, raw)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return abci.ResponseQuery{Code: 1, Log: err.Error()}
	}

	all, _ := json.Marshal(result)
	return abci.ResponseQuery{Code: 0, Value: all}
}

// Остальные методы ABCI
func (app *PromiseApp) Info(req abci.RequestInfo) abci.ResponseInfo {
	return abci.ResponseInfo{Data: "promises", Version: "0.1"}
}
func (app *PromiseApp) SetOption(req abci.RequestSetOption) abci.ResponseSetOption {
	return abci.ResponseSetOption{}
}
func (app *PromiseApp) InitChain(req abci.RequestInitChain) abci.ResponseInitChain {
	return abci.ResponseInitChain{}
}
func (app *PromiseApp) EndBlock(req abci.RequestEndBlock) abci.ResponseEndBlock {
	return abci.ResponseEndBlock{}
}
func (app *PromiseApp) ListSnapshots(req abci.RequestListSnapshots) abci.ResponseListSnapshots {
	return abci.ResponseListSnapshots{}
}
func (app *PromiseApp) OfferSnapshot(req abci.RequestOfferSnapshot) abci.ResponseOfferSnapshot {
	return abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}
}
func (app *PromiseApp) LoadSnapshotChunk(req abci.RequestLoadSnapshotChunk) abci.ResponseLoadSnapshotChunk {
	return abci.ResponseLoadSnapshotChunk{}
}
func (app *PromiseApp) ApplySnapshotChunk(req abci.RequestApplySnapshotChunk) abci.ResponseApplySnapshotChunk {
	return abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ACCEPT}
}
