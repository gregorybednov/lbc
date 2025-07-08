package persistentpeersparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ParsedEntry struct {
	ID      string
	Proto   string
	Address string
	Port    *int // nil если не указан
}

var entryPattern = regexp.MustCompile(`^([a-fA-F0-9]+)@((?:[a-zA-Z]+://)?(?:\[[^\]]+\]|[^:]+))(?:[:](\d+))?$`)

func ParseEntries(input string) ([]ParsedEntry, error) {
	entries := strings.Split(input, ",")
	var result []ParsedEntry

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		matches := entryPattern.FindStringSubmatch(entry)
		if matches == nil {
			return nil, fmt.Errorf("invalid entry: %s", entry)
		}

		id := matches[1]
		rawAddr := matches[2]
		portStr := matches[3]

		proto := ""
		address := rawAddr

		if strings.Contains(rawAddr, "://") {
			split := strings.SplitN(rawAddr, "://", 2)
			proto = split[0]
			address = split[1]
		}

		var port *int
		if portStr != "" {
			p, err := strconv.Atoi(portStr)
			if err != nil {
				return nil, fmt.Errorf("invalid port in entry: %s", entry)
			}
			port = &p
		}

		result = append(result, ParsedEntry{
			ID:      id,
			Proto:   proto,
			Address: address,
			Port:    port,
		})
	}
	return result, nil
}
