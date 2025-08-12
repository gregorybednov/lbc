package blockchain

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	types "github.com/gregorybednov/lbc_sdk"

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

func hasPrefix(id, pref string) bool { return strings.HasPrefix(id, pref+":") }

func requireIDPrefix(id, pref string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("missing %s id", pref)
	}
	if !hasPrefix(id, pref) {
		return fmt.Errorf("invalid %s id prefix", pref)
	}
	return nil
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
		// ---- Валидация содержимого композита по ER ----
		p := compound.Body.Promise
		c := compound.Body.Commitment

		// Оба тела должны присутствовать
		if p == nil || c == nil {
			return abci.ResponseCheckTx{Code: 1, Log: "compound must include promise and commitment"}
		}

		// ID-предикаты и префиксы
		if err := requireIDPrefix(p.ID, "promise"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		if err := requireIDPrefix(c.ID, "commitment"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		if err := requireIDPrefix(c.CommiterID, "commiter"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		if err := requireIDPrefix(p.BeneficiaryID, "beneficiary"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		if p.ParentPromiseID != nil {
			if err := requireIDPrefix(*p.ParentPromiseID, "promise"); err != nil {
				return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
			}
			if *p.ParentPromiseID == p.ID {
				return abci.ResponseCheckTx{Code: 2, Log: "parent_promise_id must not equal promise id"}
			}
		}

		// Базовые обязательные поля Promise
		if strings.TrimSpace(p.Text) == "" {
			return abci.ResponseCheckTx{Code: 2, Log: "promise.text is required"}
		}
		//if p.Due == 0 {
		//	return abci.ResponseCheckTx{Code: 2, Log: "promise.due is required"}
		//}
		// Commitment due
		//if c.Due == 0 {
		//	return abci.ResponseCheckTx{Code: 2, Log: "commitment.due is required"}
		//}

		// Связность по ER
		if c.PromiseID != p.ID {
			return abci.ResponseCheckTx{Code: 2, Log: "commitment.promise_id must equal promise.id"}
		}

		// Уникальность Promise.ID
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(p.ID))
			if e == badger.ErrKeyNotFound {
				return nil
			}
			return errors.New("duplicate promise ID")
		}); err != nil {
			return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
		}
		// Уникальность Commitment.ID
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(c.ID))
			if e == badger.ErrKeyNotFound {
				return nil
			}
			return errors.New("duplicate commitment ID")
		}); err != nil {
			return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
		}

		// Существование коммитера
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(c.CommiterID))
			if e == badger.ErrKeyNotFound {
				return errors.New("unknown commiter")
			}
			return nil
		}); err != nil {
			return abci.ResponseCheckTx{Code: 4, Log: err.Error()}
		}

		// Существование бенефициара
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(p.BeneficiaryID))
			if e == badger.ErrKeyNotFound {
				return errors.New("unknown beneficiary")
			}
			return nil
		}); err != nil {
			return abci.ResponseCheckTx{Code: 5, Log: err.Error()}
		}

		// Существование parent (если задан)
		if p.ParentPromiseID != nil {
			if err := app.db.View(func(txn *badger.Txn) error {
				_, e := txn.Get([]byte(*p.ParentPromiseID))
				if e == badger.ErrKeyNotFound {
					return errors.New("unknown parent promise")
				}
				return nil
			}); err != nil {
				return abci.ResponseCheckTx{Code: 6, Log: err.Error()}
			}
		}

		return abci.ResponseCheckTx{Code: 0}
	}

	// ---- Попытка ОДИНОЧНЫХ транзакций ----

	// 3.1) Совместимость: одиночный commiter (твоя старая логика)
	if body, oldErr := verifyAndExtractBody(app.db, req.Tx); oldErr == nil {
		id, ok := body["id"].(string)
		if !ok || id == "" {
			return abci.ResponseCheckTx{Code: 2, Log: "missing id"}
		}
		if err := requireIDPrefix(id, "commiter"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		// Дубликат
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(id))
			if e == badger.ErrKeyNotFound {
				return nil
			}
			return errors.New("duplicate id")
		}); err != nil {
			return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
		}
		return abci.ResponseCheckTx{Code: 0}
	}

	var single struct {
		Body      types.BeneficiaryTxBody `json:"body"`
		Signature string                  `json:"signature"`
	}
	if err2 := json.Unmarshal(req.Tx, &single); err2 == nil && single.Body.Type == "beneficiary" {
		if err := requireIDPrefix(single.Body.ID, "beneficiary"); err != nil {
			return abci.ResponseCheckTx{Code: 2, Log: err.Error()}
		}
		if strings.TrimSpace(single.Body.Name) == "" {
			return abci.ResponseCheckTx{Code: 2, Log: "beneficiary.name is required"}
		}
		// уникальность
		if err := app.db.View(func(txn *badger.Txn) error {
			_, e := txn.Get([]byte(single.Body.ID))
			if e == badger.ErrKeyNotFound {
				return nil
			}
			return errors.New("duplicate beneficiary ID")
		}); err != nil {
			return abci.ResponseCheckTx{Code: 3, Log: err.Error()}
		}
		return abci.ResponseCheckTx{Code: 0}
	}

	// Если дошли сюда — составной формат не прошёл; вернём его причину.
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
	// Попытка композита
	if compound, err := verifyCompoundTx(app.db, req.Tx); err == nil {
		if app.currentBatch == nil {
			app.currentBatch = app.db.NewTransaction(true)
		}
		if compound.Body.Promise != nil {
			data, _ := json.Marshal(compound.Body.Promise)
			if err := app.currentBatch.Set([]byte(compound.Body.Promise.ID), data); err != nil {
				return abci.ResponseDeliverTx{Code: 1, Log: "failed to save promise"}
			}
		}
		if compound.Body.Commitment != nil {
			data, _ := json.Marshal(compound.Body.Commitment)
			if err := app.currentBatch.Set([]byte(compound.Body.Commitment.ID), data); err != nil {
				return abci.ResponseDeliverTx{Code: 1, Log: "failed to save commitment"}
			}
		}
		return abci.ResponseDeliverTx{Code: 0}
	}

	// Одиночный commiter (как раньше)
	{
		var outer struct {
			Body      types.CommiterTxBody `json:"body"`
			Signature string               `json:"signature"`
		}
		if err := json.Unmarshal(req.Tx, &outer); err == nil && outer.Body.Type == "commiter" {
			// сигнатуру проверяем прежней функцией
			if _, vErr := verifyAndExtractBody(app.db, req.Tx); vErr != nil {
				return abci.ResponseDeliverTx{Code: 1, Log: vErr.Error()}
			}
			if app.currentBatch == nil {
				app.currentBatch = app.db.NewTransaction(true)
			}
			data, _ := json.Marshal(outer.Body)
			if err := app.currentBatch.Set([]byte(outer.Body.ID), data); err != nil {
				return abci.ResponseDeliverTx{Code: 1, Log: err.Error()}
			}
			return abci.ResponseDeliverTx{Code: 0}
		}
	}

	// Одиночный beneficiary
	{
		var outer struct {
			Body      types.BeneficiaryTxBody `json:"body"`
			Signature string                  `json:"signature"`
		}
		if err := json.Unmarshal(req.Tx, &outer); err == nil && outer.Body.Type == "beneficiary" {
			// (пока без проверки подписи — можно добавить политику позже)
			if app.currentBatch == nil {
				app.currentBatch = app.db.NewTransaction(true)
			}
			data, _ := json.Marshal(outer.Body)
			if err := app.currentBatch.Set([]byte(outer.Body.ID), data); err != nil {
				return abci.ResponseDeliverTx{Code: 1, Log: err.Error()}
			}
			return abci.ResponseDeliverTx{Code: 0}
		}
	}

	return abci.ResponseDeliverTx{Code: 1, Log: "invalid tx format"}
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
