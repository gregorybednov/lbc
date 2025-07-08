package main

import (
	"bytes"
	"fmt"

	"github.com/dgraph-io/badger"
	abci "github.com/tendermint/tendermint/abci/types"
)

type KVStoreApplication struct {
	db           *badger.DB
	currentBatch *badger.Txn
}

func NewKVStoreApplication(db *badger.DB) *KVStoreApplication {
	return &KVStoreApplication{db: db}
}

// Проверка транзакции (CheckTx)
func (app *KVStoreApplication) isValid(tx []byte) uint32 {
	parts := bytes.Split(tx, []byte("="))
	if len(parts) != 2 {
		return 1 // неверный формат
	}
	key, value := parts[0], parts[1]

	// Проверяем, существует ли уже такая запись
	err := app.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return nil // ключ не найден, все ок
			}
			return err
		}

		return item.Value(func(val []byte) error {
			if bytes.Equal(val, value) {
				return fmt.Errorf("duplicate key-value")
			}
			return nil
		})
	})

	if err != nil {
		if err.Error() == "duplicate key-value" {
			return 2 // дубликат найден
		}
		// любая другая ошибка
		return 1
	}

	return 0 // все проверки пройдены
}

func (app *KVStoreApplication) CheckTx(req abci.RequestCheckTx) abci.ResponseCheckTx {
	code := app.isValid(req.Tx)
	return abci.ResponseCheckTx{Code: code, GasWanted: 1}
}

// Начало блока
func (app *KVStoreApplication) BeginBlock(req abci.RequestBeginBlock) abci.ResponseBeginBlock {
	// If there's an existing batch for some reason, discard it first
	if app.currentBatch != nil {
		app.currentBatch.Discard()
	}
	app.currentBatch = app.db.NewTransaction(true)
	return abci.ResponseBeginBlock{}
}

// Применение транзакции
func (app *KVStoreApplication) DeliverTx(req abci.RequestDeliverTx) abci.ResponseDeliverTx {
	code := app.isValid(req.Tx)
	if code != 0 {
		return abci.ResponseDeliverTx{Code: code}
	}

	parts := bytes.Split(req.Tx, []byte("="))
	if app.currentBatch == nil {
		// In case BeginBlock wasn't called or batch was discarded
		app.currentBatch = app.db.NewTransaction(true)
	}

	err := app.currentBatch.Set(parts[0], parts[1])
	if err != nil {
		return abci.ResponseDeliverTx{
			Code: 1,
			Log:  fmt.Sprintf("Failed to set key: %v", err),
		}
	}

	return abci.ResponseDeliverTx{Code: 0}
}

// Завершение блока и фиксация
func (app *KVStoreApplication) Commit() abci.ResponseCommit {
	if app.currentBatch != nil {
		err := app.currentBatch.Commit()
		if err != nil {
			// Log error but continue - in a real application, you might want
			// to handle this more gracefully
			fmt.Printf("Error committing batch: %v\n", err)
		}
		app.currentBatch = nil
	}
	return abci.ResponseCommit{Data: []byte{}}
}

// Обслуживание запросов Query
func (app *KVStoreApplication) Query(req abci.RequestQuery) abci.ResponseQuery {
	resp := abci.ResponseQuery{Code: 0}

	err := app.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(req.Data)
		if err != nil {
			return err
		}

		return item.Value(func(val []byte) error {
			resp.Value = val
			return nil
		})
	})

	if err != nil {
		resp.Code = 1
		resp.Log = err.Error()
	}

	return resp
}

// Добавляем недостающие методы ABCI интерфейса

// Info возвращает информацию о приложении
func (app *KVStoreApplication) Info(req abci.RequestInfo) abci.ResponseInfo {
	return abci.ResponseInfo{
		Data:             "kvstore",
		Version:          "1.0.0",
		AppVersion:       1,
		LastBlockHeight:  0,
		LastBlockAppHash: []byte{},
	}
}

// SetOption устанавливает опцию приложения
func (app *KVStoreApplication) SetOption(req abci.RequestSetOption) abci.ResponseSetOption {
	return abci.ResponseSetOption{Code: 0}
}

// InitChain инициализирует блокчейн
func (app *KVStoreApplication) InitChain(req abci.RequestInitChain) abci.ResponseInitChain {
	return abci.ResponseInitChain{}
}

// EndBlock сигнализирует о конце блока
func (app *KVStoreApplication) EndBlock(req abci.RequestEndBlock) abci.ResponseEndBlock {
	return abci.ResponseEndBlock{}
}

// ListSnapshots возвращает список доступных снапшотов
func (app *KVStoreApplication) ListSnapshots(req abci.RequestListSnapshots) abci.ResponseListSnapshots {
	return abci.ResponseListSnapshots{}
}

// OfferSnapshot предлагает снапшот приложению
func (app *KVStoreApplication) OfferSnapshot(req abci.RequestOfferSnapshot) abci.ResponseOfferSnapshot {
	return abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}
}

// LoadSnapshotChunk загружает часть снапшота
func (app *KVStoreApplication) LoadSnapshotChunk(req abci.RequestLoadSnapshotChunk) abci.ResponseLoadSnapshotChunk {
	return abci.ResponseLoadSnapshotChunk{}
}

// ApplySnapshotChunk применяет часть снапшота
func (app *KVStoreApplication) ApplySnapshotChunk(req abci.RequestApplySnapshotChunk) abci.ResponseApplySnapshotChunk {
	return abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ACCEPT}
}
