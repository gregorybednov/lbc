package yggdrasil

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gologme/log"
	"github.com/spf13/viper"

	yggConfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
)

// TestConnectivity starts a temporary Yggdrasil node using the provided
// configuration and waits a short time for peer connections. It returns an
// error if the node fails to start or no peers connect.
func TestConnectivity(config *viper.Viper) error {
	ygg := config.Sub("yggdrasil")
	if ygg == nil {
		return fmt.Errorf("no [yggdrasil] section in config")
	}

	cfg := yggConfig.GenerateConfig()
	cfg.AdminListen = ygg.GetString("admin_listen")
	cfg.Listen = ygg.GetStringSlice("listen")
	if ygg.GetString("peers") == "auto" {
		publicPeers := RandomPick(GetClosestPeers(getPublicPeers(), 20), 3)
		var urls []string
		for _, u := range publicPeers {
			urls = append(urls, u.String())
		}
		cfg.Peers = urls
	} else {
		cfg.Peers = ygg.GetStringSlice("peers")
	}
	cfg.AllowedPublicKeys = ygg.GetStringSlice("allowed-public-keys")
	cfg.PrivateKeyPath = ygg.GetString("private-key-file")

	logger := log.Default()
	n := &node{}

	options := []core.SetupOption{
		core.NodeInfo(cfg.NodeInfo),
		core.NodeInfoPrivacy(cfg.NodeInfoPrivacy),
	}
	for _, addr := range cfg.Listen {
		options = append(options, core.ListenAddress(addr))
	}
	for _, peer := range cfg.Peers {
		options = append(options, core.Peer{URI: peer})
	}
	for intf, peers := range cfg.InterfacePeers {
		for _, peer := range peers {
			options = append(options, core.Peer{URI: peer, SourceInterface: intf})
		}
	}
	for _, allowed := range cfg.AllowedPublicKeys {
		k, err := hex.DecodeString(allowed)
		if err != nil {
			return err
		}
		options = append(options, core.AllowedPublicKey(k[:]))
	}

	var err error
	n.core, err = core.New(cfg.Certificate, logger, options...)
	if err != nil {
		return err
	}
	defer n.core.Stop()

	// Start admin socket to query peers.
	//adminOpts := []adminapi.SetupOption{
	//	adminapi.ListenAddress(cfg.AdminListen),
	//}
	//n.admin, err = adminapi.New(n.core, logger, adminOpts...)
	//if err != nil {
	//	return err
	//}
	//n.admin.SetupAdminHandlers()
	//defer n.admin.Stop()

	// Give the node some time to establish connections.
	time.Sleep(5 * time.Second)

	peers := n.core.GetPeers()
	if len(peers) == 0 {
		return fmt.Errorf("no peers connected")
	}
	return nil
}
