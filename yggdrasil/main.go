package yggdrasil

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/gologme/log"
	"github.com/spf13/viper"

	"github.com/yggdrasil-network/yggdrasil-go/src/admin"
	yggConfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
	"github.com/yggdrasil-network/yggdrasil-go/src/core"
	"github.com/yggdrasil-network/yggdrasil-go/src/multicast"
	"github.com/yggdrasil-network/yggstack/src/netstack"
	"github.com/yggdrasil-network/yggstack/src/types"
)

type node struct {
	core       *core.Core
	multicast  *multicast.Multicast
	admin      *admin.AdminSocket
	socks5Tcp  net.Listener
	socks5Unix net.Listener
}

type TCPLocalListenerMapping struct {
	Listen *net.TCPListener
	Mapped net.TCPAddr
}

type TCPRemoteListenerMapping struct {
	Listen net.TCPAddr
	Mapped *net.TCPListener
}

// The main function is responsible for configuring and starting Yggdrasil.
func Yggdrasil(config *viper.Viper, ch chan string) {
	var remoteTcp types.TCPRemoteMappings
	socks := ""

	ygg := config.Sub("yggdrasil")
	if ygg == nil {
		log.Errorln("No [yggdrasil] config found")
		return
	}
	p2p := config.Sub("p2p")
	if p2p == nil {
		log.Errorln("No [p2p] config found")
		return
	}

	var peersList []string
	var yggList []string

	// Create a new logger that logs output to stdout.
	var logger *log.Logger
	if logger == nil {
		logger = log.New(os.Stdout, "", log.Flags())
	}

	laddr := p2p.GetString("laddr")
	remoteTcp.Set(laddr)
	ch <- remoteTcp[0].Mapped.String()

	peers := p2p.GetString("persistent_peers")
	parsed, err := ParseEntries(peers)
	if err != nil {
		parsed = []ParsedEntry{}
		ch <- ""
		log.Warnln("Warning: persistent peers has an error")
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

	logger.Infof("Yggdrasil peers: %s", cfg.Peers)

	cfg.AllowedPublicKeys = ygg.GetStringSlice("allowed-public-keys")
	cfg.PrivateKeyPath = ygg.GetString("private-key-file")

	// Catch interrupts from the operating system to exit gracefully.
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	n := &node{}

	// Setup the Yggdrasil node itself.
	{
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
				panic(err)
			}
			options = append(options, core.AllowedPublicKey(k[:]))
		}

		var err error
		if n.core, err = core.New(cfg.Certificate, logger, options...); err != nil {
			panic(err)
		}
		address, subnet := n.core.Address(), n.core.Subnet()
		publicstr := hex.EncodeToString(n.core.PublicKey())
		logger.Printf("Your public key is %s", publicstr)
		logger.Printf("Your IPv6 address is %s", address.String())
		logger.Printf("Your IPv6 subnet is %s", subnet.String())
		logger.Printf("Your Yggstack resolver name is %s%s", publicstr, types.NameMappingSuffix)
	}

	// Setup the admin socket.
	{
		options := []admin.SetupOption{
			admin.ListenAddress(cfg.AdminListen),
		}
		if cfg.LogLookups {
			options = append(options, admin.LogLookups{})
		}
		var err error
		if n.admin, err = admin.New(n.core, logger, options...); err != nil {
			panic(err)
		}
		if n.admin != nil {
			n.admin.SetupAdminHandlers()
		}
	}

	// Setup the multicast module.
	{
		options := []multicast.SetupOption{}
		for _, intf := range cfg.MulticastInterfaces {
			options = append(options, multicast.MulticastInterface{
				Regex:    regexp.MustCompile(intf.Regex),
				Beacon:   intf.Beacon,
				Listen:   intf.Listen,
				Port:     intf.Port,
				Priority: uint8(intf.Priority),
				Password: intf.Password,
			})
		}
		var err error
		if n.multicast, err = multicast.New(n.core, logger, options...); err != nil {
			panic(err)
		}
		if n.admin != nil && n.multicast != nil {
			n.multicast.SetupAdminHandlers(n.admin)
		}
	}

	// Setup Yggdrasil netstack
	s, err := netstack.CreateYggdrasilNetstack(n.core)
	if err != nil {
		panic(err)
	}

	// Create local TCP mappings (forwarding connections from local port
	// to remote Yggdrasil node)
	{
		for _, p := range parsed {
			// 1) создаём listener на порту 0 — получим динамический свободный
			laddr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
			listener, err := net.ListenTCP("tcp", laddr)
			if err != nil {
				panic(err)
			}

			// извлекаем фактический порт
			realPort := listener.Addr().(*net.TCPAddr).Port

			// 2) собираем строку для ygg-маппинга: "127.0.0.1:<realPort>:<peerIP>:<peerPort>"
			remotePort := ""
			if p.Port != nil {
				remotePort = fmt.Sprintf(":%d", *p.Port)
			}
			if p.Proto == "ygg" {
				yggList = append(yggList,
					fmt.Sprintf("127.0.0.1:%d:%s%s", realPort, p.Address, remotePort),
				)
			}

			// 3) строка для Tendermint PersistentPeers: "<peerID>@127.0.0.1:<realPort>"
			peersList = append(peersList,
				fmt.Sprintf("%s@127.0.0.1:%d", p.ID, realPort),
			)

			// запускаем горутину проксирования далее уже по этому listener
			go func(l *net.TCPListener, mapped net.TCPAddr) {
				logger.Infof("Mapping local TCP port %d to Ygg %s", realPort, mapped.String())
				for {
					c, err := l.Accept()
					if err != nil {
						panic(err)
					}
					r, err := s.DialTCP(&mapped)
					if err != nil {
						logger.Errorf("Failed to connect to %s: %s", mapped.String(), err)
						_ = c.Close()
						continue
					}
					go types.ProxyTCP(n.core.MTU(), c, r)
				}
			}(listener, net.TCPAddr{IP: net.ParseIP(p.Address), Port: *p.Port})
		}

		// а вот здесь — динамические peer-ы
		ch <- strings.Join(peersList, ",")
	}

	// Create remote TCP mappings (forwarding connections from Yggdrasil
	// node to local port)
	{

		for _, mapping := range remoteTcp {

			go func(mapping types.TCPMapping) {

				listener, err := s.ListenTCP(mapping.Listen)
				if err != nil {
					panic(err)
				}
				logger.Infof("Mapping Yggdrasil TCP port %d to %s", mapping.Listen.Port, mapping.Mapped)
				for {
					c, err := listener.Accept()
					if err != nil {
						panic(err)
					}
					r, err := net.DialTCP("tcp", nil, mapping.Mapped)
					if err != nil {
						logger.Errorf("Failed to connect to %s: %s", mapping.Mapped, err)
						_ = c.Close()
						continue
					}
					go types.ProxyTCP(n.core.MTU(), c, r)
				}
			}(mapping)
		}
	}

	// Block until we are told to shut down.
	<-ctx.Done()

	// Shut down the node.
	_ = n.admin.Stop()
	_ = n.multicast.Stop()
	if n.socks5Unix != nil {
		_ = n.socks5Unix.Close()
		_ = os.RemoveAll(socks)
		logger.Infof("Stopped SOCKS5 UNIX socket listener")
	}
	if n.socks5Tcp != nil {
		_ = n.socks5Tcp.Close()
		logger.Infof("Stopped SOCKS5 TCP listener")
	}
	n.core.Stop()
}
