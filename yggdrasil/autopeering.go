package yggdrasil

import (
	"context"
	"io/fs"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
)

type Peer struct {
	URL     url.URL
	Online  bool
	Latency time.Duration
}

var PublicPeers string

const repoURL = "https://github.com/yggdrasil-network/public-peers"
const localPeersPath = "peers.txt"

func readPeersFile(path string) []url.URL {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var peers []url.URL
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)

		if line != "" {
			url, err := url.Parse(line)
			if err != nil {
				panic(err)
			}
			peers = append(peers, *url)
		}
	}
	return peers
}

// GetPublicPeers fetches the public-peers repository and returns a list of peer
// connection strings. If fetching fails, it falls back to reading peers from
// peers.txt in the current working directory. It returns an empty slice if no
// peers can be retrieved.
func getPublicPeers() []url.URL {
	tempDir, err := os.MkdirTemp("", "public-peers-*")
	if err != nil {
		return readPeersFile(localPeersPath)
	}
	defer os.RemoveAll(tempDir)

	_, err = git.PlainCloneContext(context.Background(), tempDir, false, &git.CloneOptions{URL: repoURL, Depth: 1})
	if err != nil {
		return readPeersFile(localPeersPath)
	}

	var peers []url.URL
	re := regexp.MustCompile(`(?m)(tcp|tls)://[^\s` + "`" + `]+`)
	filepath.WalkDir(tempDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("walk error: %v", err)
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") || d.Name() == "README.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			urlStr := strings.TrimSpace(m[0])
			url, err := url.Parse(urlStr)
			if err != nil {
				panic(err)
			}
			peers = append(peers, *url)
		}
		return nil
	})

	if len(peers) == 0 {
		return readPeersFile(localPeersPath)
	}
	return peers
}

// Get n online peers with best latency from a peer list
func GetClosestPeers(peerList []url.URL, n int) []url.URL {
	var result []url.URL
	onlinePeers := testPeers(peerList)

	// Filter online peers
	x := 0
	for _, p := range onlinePeers {
		if p.Online {
			onlinePeers[x] = p
			x++
		}
	}
	onlinePeers = onlinePeers[:x]

	sort.Slice(onlinePeers, func(i, j int) bool {
		return onlinePeers[i].Latency < onlinePeers[j].Latency
	})

	for i := 0; i < len(onlinePeers); i++ {
		if len(result) == n {
			break
		}
		result = append(result, onlinePeers[i].URL)
	}

	return result
}

// Pick n random peers from a list
func RandomPick(peerList []url.URL, n int) []url.URL {
	if len(peerList) <= n {
		return peerList
	}

	var res []url.URL
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	for _, i := range r.Perm(n) {
		res = append(res, peerList[i])
	}

	return res
}

const defaultTimeout time.Duration = time.Duration(3) * time.Second

func testPeers(peers []url.URL) []Peer {
	var res []Peer
	results := make(chan Peer)

	for _, p := range peers {
		go testPeer(p, results)
	}

	for range peers {
		res = append(res, <-results)
	}

	return res
}

func testPeer(peer url.URL, results chan Peer) {
	p := Peer{peer, false, 0.0}
	p.Online = false
	t0 := time.Now()

	var network string
	if peer.Scheme == "tcp" || peer.Scheme == "tls" {
		network = "tcp"
	} else { // skip, not supported yet
		results <- p
		return
	}

	conn, err := net.DialTimeout(network, peer.Host, defaultTimeout)
	if err == nil {
		t1 := time.Now()
		conn.Close()
		p.Online = true
		p.Latency = t1.Sub(t0)
	}
	results <- p
}
