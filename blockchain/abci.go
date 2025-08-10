package blockchain

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"lbc/blockchain/types"
	"strings"

	"github.com/dgraph-io/badger"
	abci "github.com/tendermint/tendermint/abci/types"
)

type PromiseApp struct {
	db           *badger.DB
	currentBatch *badger.Txn
}

type compoundTxRaw struct {
	Body      json.RawMessage `json:"body"`
	Signature string          `json:"signature"`
}

type compoundBody struct {
	Promise    *types.PromiseTxBody    `json:"promise"`
	Commitment *types.CommitmentTxBody `json:"commitment"`
}

func NewPromiseApp(db *badger.DB) *PromiseApp {
	return &PromiseApp{db: db}
}

func verifyAndExtractBody(db *badger.DB, tx []byte) (map[string]interface{}, error) {
	var outer struct {
		Body      types.CommiterTxBody `json:"body"`
		Signature string               `json:"signature"`
	}

	if err := json.Unmarshal(tx, &outer); err != nil {
		return nil, errors.New("invalid JSON wrapper")
	}

	msg, err := json.Marshal(outer.Body)
	if err != nil {
		return nil, err
	}

	sig, err := base64.StdEncoding.DecodeString(outer.Signature)
	if err != nil {
		return nil, errors.New("invalid signature base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}

	pubkeyB64 := strings.TrimSpace(outer.Body.CommiterPubKey)
	if pubkeyB64 == "" {
		return nil, errors.New("missing commiter pubkey")
	}
	pubkey, err := base64.StdEncoding.DecodeString(pubkeyB64)
	if err != nil {
		return nil, errors.New("invalid pubkey base64")
	}
	if len(pubkey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid pubkey length: got %d, want %d", len(pubkey), ed25519.PublicKeySize)
	}

	if !ed25519.Verify(pubkey, msg, sig) {
		return nil, errors.New("signature verification failed")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(msg, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (app *PromiseApp) CheckTx(req abci.RequestCheckTx) abci.ResponseCheckTx {
	compound, err := verifyCompoundTx(app.db, req.Tx)
	if err == nil {
		// Проверка на уникальность Promise.ID
		if compound.Body.Promise != nil {
			err := app.db.View(func(txn *badger.Txn) error {
				_, err := txn.Get([]byte(compound.Body.Promise.ID))
				if err == badger.ErrKeyNotFound {
					return nil
				}
				return errors.New("duplicate promise ID")
			})
			if err != nil {
				return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
			}
		}

		// Проверка на уникальность Commitment.ID
		if compound.Body.Commitment != nil {
			err := app.db.View(func(txn *badger.Txn) error {
				_, err := txn.Get([]byte(compound.Body.Commitment.ID))
				if err == badger.ErrKeyNotFound {
					return nil
				}
				return errors.New("duplicate commitment ID")
			})
			if err != nil {
				return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
			}
		}

		// Проверка на существование коммитера
		if compound.Body.Commitment != nil {
			err := app.db.View(func(txn *badger.Txn) error {
				_, err := txn.Get([]byte(compound.Body.Commitment.CommiterID))
				if err == badger.ErrKeyNotFound {
					return errors.New("unknown commiter")
				}
				return nil
			})
			if err != nil {
				return abci.ResponseCheckTx{Code: 4, Log: err.Error()}
			}
		}

		return abci.ResponseCheckTx{Code: 0}
	}

	// Попытка старого формата только если он реально валиден;
	// иначе возвращаем исходную причину из verifyCompoundTx.
	if body, oldErr := verifyAndExtractBody(app.db, req.Tx); oldErr == nil {
		id, ok := body["id"].(string)
		if !ok || id == "" {
			return abci.ResponseCheckTx{Code: 2, Log: "missing id"}
		}
		// Проверка на дубликат
		err := app.db.View(func(txn *badger.Txn) error {
			_, err := txn.Get([]byte(id))
			if err == badger.ErrKeyNotFound {
				return nil
			}
			return errors.New("duplicate id")
		})
		if err != nil {
			return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
		}
		return abci.ResponseCheckTx{Code: 0}
	}

	// Здесь: составной формат не прошёл — вернём ПРИЧИНУ.
	return abci.ResponseCheckTx{Code: 1, Log: err.Error()}
}

func (app *PromiseApp) BeginBlock(_ abci.RequestBeginBlock) abci.ResponseBeginBlock {
	if app.currentBatch != nil {
		app.currentBatch.Discard()
	}
	app.currentBatch = app.db.NewTransaction(true)
	return abci.ResponseBeginBlock{}
}

func (app *PromiseApp) DeliverTx(req abci.RequestDeliverTx) abci.ResponseDeliverTx {
	compound, err := verifyCompoundTx(app.db, req.Tx)
	if err != nil {
		outer := struct {
			Body      types.CommiterTxBody `json:"body"`
			Signature string               `json:"signature"`
		}{}
		if err := json.Unmarshal(req.Tx, &outer); err != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: "invalid tx format"}
		}
		// Валидация подписи/ключа одиночного коммитера перед записью
		if _, vErr := verifyAndExtractBody(app.db, req.Tx); vErr != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: vErr.Error()}
		}

		id := outer.Body.ID
		if id == "" {
			return abci.ResponseDeliverTx{Code: 1, Log: "missing id"}
		}

		if app.currentBatch == nil {
			app.currentBatch = app.db.NewTransaction(true)
		}

		data, err := json.Marshal(outer.Body)
		if err != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: err.Error()}
		}
		if err = app.currentBatch.Set([]byte(id), data); err != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: err.Error()}
		}
		return abci.ResponseDeliverTx{Code: 0}
	}

	if app.currentBatch == nil {
		app.currentBatch = app.db.NewTransaction(true)
	}

	if compound.Body.Promise != nil {
		data, _ := json.Marshal(compound.Body.Promise)
		err := app.currentBatch.Set([]byte(compound.Body.Promise.ID), data)
		if err != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: "failed to save promise"}
		}
	}
	if compound.Body.Commitment != nil {
		data, _ := json.Marshal(compound.Body.Commitment)
		err := app.currentBatch.Set([]byte(compound.Body.Commitment.ID), data)
		if err != nil {
			return abci.ResponseDeliverTx{Code: 1, Log: "failed to save commitment"}
		}
	}

	return abci.ResponseDeliverTx{Code: 0}
}

func verifyCompoundTx(db *badger.DB, tx []byte) (*types.CompoundTx, error) {
	// 1) Разобрать внешний конверт, body оставить сырым
	var outerRaw compoundTxRaw
	if err := json.Unmarshal(tx, &outerRaw); err != nil {
		return nil, errors.New("invalid compound tx JSON")
	}
	if len(outerRaw.Body) == 0 {
		return nil, errors.New("missing body")
	}

	// 2) Вынуть commiter_id из body, не парся всё
	var tiny struct {
		Commitment *struct {
			CommiterID string `json:"commiter_id"`
		} `json:"commitment"`
	}
	if err := json.Unmarshal(outerRaw.Body, &tiny); err != nil {
		return nil, errors.New("invalid body JSON")
	}
	if tiny.Commitment == nil || strings.TrimSpace(tiny.Commitment.CommiterID) == "" {
		return nil, errors.New("missing commitment")
	}
	commiterID := tiny.Commitment.CommiterID

	// 3) Достать коммитера из БД и получить публичный ключ
	var commiterData []byte
	err := db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(commiterID))
		if err != nil {
			return errors.New("unknown commiter")
		}
		return item.Value(func(v []byte) error {
			commiterData = append([]byte{}, v...)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	var commiter types.CommiterTxBody
	if err := json.Unmarshal(commiterData, &commiter); err != nil {
		return nil, errors.New("corrupted commiter record")
	}
	pubkeyB64 := strings.TrimSpace(commiter.CommiterPubKey)
	if pubkeyB64 == "" {
		return nil, errors.New("missing commiter pubkey")
	}
	pubkey, err := base64.StdEncoding.DecodeString(pubkeyB64)
	if err != nil {
		return nil, errors.New("invalid pubkey base64")
	}
	if len(pubkey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid pubkey length: got %d, want %d", len(pubkey), ed25519.PublicKeySize)
	}

	// 4) Проверка подписи над СЫРЫМИ БАЙТАМИ body (как клиент подписывал)
	sig, err := base64.StdEncoding.DecodeString(outerRaw.Signature)
	if err != nil {
		return nil, errors.New("invalid signature base64")
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid signature length: got %d, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pubkey, outerRaw.Body, sig) {
		return nil, errors.New("signature verification failed")
	}

	// 5) Только после успешной проверки — распарсить body в наши структуры
	var body compoundBody
	if err := json.Unmarshal(outerRaw.Body, &body); err != nil {
		return nil, errors.New("invalid body JSON")
	}

	// 6) Вернуть в привычной форме
	return &types.CompoundTx{
		Body: struct {
			Promise    *types.PromiseTxBody    `json:"promise"`
			Commitment *types.CommitmentTxBody `json:"commitment"`
		}{
			Promise:    body.Promise,
			Commitment: body.Commitment,
		},
		Signature: outerRaw.Signature,
	}, nil
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
