// Package idtools is forked from https://github.com/moby/moby/tree/298ba5b13150bfffe8414922a951a7a793276d31/pkg/idtools
package idtools

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type SubIDRange struct {
	Start  int
	Length int
}

const (
	subuidFileName = "/etc/subuid"
	subgidFileName = "/etc/subgid"
)

func GetSubIDRanges(uid int, username string) ([]SubIDRange, []SubIDRange, error) {
	subuidRanges, err := parseSubuid(uid, username)
	if err != nil {
		return nil, nil, err
	}
	subgidRanges, err := parseSubgid(uid, username)
	if err != nil {
		return nil, nil, err
	}
	if len(subuidRanges) == 0 {
		return nil, nil, fmt.Errorf("No subuid ranges found for user %d (%q)", uid, username)
	}
	if len(subgidRanges) == 0 {
		return nil, nil, fmt.Errorf("No subgid ranges found for user %d (%q)", uid, username)
	}
	return subuidRanges, subgidRanges, nil
}

func parseSubuid(uid int, username string) ([]SubIDRange, error) {
	return parseSubidFile(subuidFileName, uid, username)
}

func parseSubgid(uid int, username string) ([]SubIDRange, error) {
	return parseSubidFile(subgidFileName, uid, username)
}

// parseSubidFile will read the appropriate file (/etc/subuid or /etc/subgid)
// and return all found ranges for a specified user. username is optional.
func parseSubidFile(path string, uid int, username string) ([]SubIDRange, error) {
	uidS := strconv.Itoa(uid)
	var rangeList []SubIDRange

	subidFile, err := os.Open(path)
	if err != nil {
		return rangeList, err
	}
	defer subidFile.Close()

	s := bufio.NewScanner(subidFile)
	for s.Scan() {
		text := strings.TrimSpace(s.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		parts := strings.Split(text, ":")
		if len(parts) != 3 {
			return rangeList, fmt.Errorf("Cannot parse subuid/gid information: Format not correct for %s file", path)
		}
		if parts[0] == uidS || (username != "" && parts[0] == username) {
			startid, err := strconv.Atoi(parts[1])
			if err != nil {
				return rangeList, fmt.Errorf("String to int conversion failed during subuid/gid parsing of %s: %v", path, err)
			}
			length, err := strconv.Atoi(parts[2])
			if err != nil {
				return rangeList, fmt.Errorf("String to int conversion failed during subuid/gid parsing of %s: %v", path, err)
			}
			rangeList = append(rangeList, SubIDRange{startid, length})
		}
	}
	return rangeList, s.Err()
}
