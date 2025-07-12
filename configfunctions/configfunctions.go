package configfunctions

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"lbc/yggdrasil"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/viper"
	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	tmTypes "github.com/tendermint/tendermint/types"
)

var (
	yggListenPort = 4224
	yggKeyPath    = flag.String("ygg-key", "./config/yggdrasil.key", "Path to Yggdrasil key file")
)

func InitTendermintFiles(config *cfg.Config) error {
	if err := os.MkdirAll(filepath.Dir(config.PrivValidatorKeyFile()), 0700); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Join(config.RootDir, "data"), 0700); err != nil {
		return err
	}

	pv := privval.GenFilePV(
		config.PrivValidatorKeyFile(),
		config.PrivValidatorStateFile(),
	)

	if _, err := p2p.LoadOrGenNodeKey(config.NodeKeyFile()); err != nil {
		return err
	}

	key, err := pv.GetPubKey()
	if err != nil {
		return err
	}

	// Genesis
	genDoc := &tmTypes.GenesisDoc{
		ChainID:         "lbc-chain",
		GenesisTime:     time.Now(),
		ConsensusParams: tmTypes.DefaultConsensusParams(),
		Validators: []tmTypes.GenesisValidator{
			{
				Address: key.Address(),
				PubKey:  key,
				Power:   10,
				Name:    config.Moniker,
			},
		},
		AppHash: []byte{},
	}

	pv.Save()
	return genDoc.SaveAs(config.GenesisFile())
}

func writeYggdrasilKey(path string) {
	bytes := yggdrasil.GeneratePrivateKey()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return
	}

	hexKey := hex.EncodeToString(bytes[:])
	os.WriteFile(path, []byte(hexKey), 0600)
}

func WriteConfig(config *cfg.Config, configPath *string, nodeInfo p2p.NodeInfo) *viper.Viper {
	writeYggdrasilKey(*yggKeyPath)
	pubkey, err := yggdrasil.GetPublicKey(*yggKeyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yggdrasil key not found: %v\n", err)
		os.Exit(1)
	}
	domain := hex.EncodeToString(pubkey) + ".pk.ygg"
	fmt.Printf("Yggdrasil node domain: %s\n", domain)

	v := viper.New()
	v.SetConfigFile(*configPath)
	v.SetConfigType("toml")

	v.Set("moniker", config.Moniker)
	v.Set("db_backend", config.DBBackend)
	v.Set("db_dir", config.DBDir())
	v.Set("log_level", config.LogLevel)
	v.Set("log_format", config.LogFormat)
	v.Set("genesis_file", config.GenesisFile())
	v.Set("node_key_file", config.NodeKeyFile())
	v.Set("abci", config.ABCI)
	v.Set("filter_peers", config.FilterPeers)

	v.Set("priv_validator", map[string]any{
		"key_file":                config.PrivValidatorKeyFile(),
		"state_file":              config.PrivValidatorStateFile(),
		"laddr":                   config.PrivValidatorListenAddr,
		"client_certificate_file": "",
		"client_key_file":         "",
		"root_ca_file":            "",
	})

	v.Set("yggdrasil", map[string]any{
		"admin_listen":        "none",
		"peers":               "auto",
		"allowed_public_keys": []string{},
		"private_key_file":    *yggKeyPath,
	})

	v.Set("p2p", map[string]interface{}{
		"use_legacy":       false,
		"queue_type":       "priority",
		"laddr":            strconv.Itoa(yggListenPort) + ":127.0.0.1:8000",
		"external_address": "", // will be set automatically by Tendermint if needed
		"upnp":             false,
		"bootstrap_peers":  "",
		"persistent_peers": config.P2P.PersistentPeers,
		"addr_book_file":   "config/addrbook.json",
		"addr_book_strict": true,
	})

	nodeId := nodeInfo.ID()
	myPeer := yggdrasil.GetYggdrasilAddress(v, nil)
	config.P2P.PersistentPeers = string(nodeId) + "@ygg://[" + myPeer + "]:" + strconv.Itoa(yggListenPort)

	err = v.WriteConfigAs(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		os.Exit(1)
	}

	return v
}

func ReadConfig(configFile string) (*cfg.Config, error) {
	config := cfg.DefaultConfig()
	config.RootDir = filepath.Dir(filepath.Dir(configFile))
	viper.SetConfigFile(configFile)
	viper.SetConfigType("toml")

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("viper read config: %w", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("viper unmarshal: %w", err)
	}

	if err := config.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("config invalid: %w", err)
	}

	return config, nil
}

func DefaultConfig() *cfg.Config {
	return cfg.DefaultConfig()
}

func UpdateGenesisJson(nodeInfo p2p.NodeInfo, v *viper.Viper, defaultConfigDirectoryPath string) {
	file, err := os.ReadFile(defaultConfigDirectoryPath + "/genesis.json")
	if err != nil {
		panic(err)
	}

	var dat map[string]any
	if err := json.Unmarshal(file, &dat); err != nil {
		panic(err)
	}

	myPeer := yggdrasil.GetYggdrasilAddress(v, nil)

	p2peers := fmt.Sprintf("%s@ygg://[%s]:%d", nodeInfo.ID(), myPeer, yggListenPort)
	dat["p2peers"] = p2peers

	out, err := json.MarshalIndent(dat, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(defaultConfigDirectoryPath+"/genesis.json", out, 0o644); err != nil {
		panic(err)
	}
}
