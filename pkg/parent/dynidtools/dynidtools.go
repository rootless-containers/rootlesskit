package dynidtools

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rootless-containers/rootlesskit/pkg/parent/idtools"
	"github.com/sirupsen/logrus"
)

func withoutDuplicates(sliceList []idtools.SubIDRange) []idtools.SubIDRange {
	seenKeys := make(map[idtools.SubIDRange]bool)
	var list []idtools.SubIDRange
	for _, item := range sliceList {
		if _, value := seenKeys[item]; !value {
			seenKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}

func GetSubIDRanges(uid int, username string) ([]idtools.SubIDRange, []idtools.SubIDRange, error) {
	getsubidsExeName := "getsubids"
	if v := os.Getenv("GETSUBIDS"); v != "" {
		getsubidsExeName = v
	}
	getsubidsExe, err := exec.LookPath(getsubidsExeName)
	if err != nil {
		return nil, nil, fmt.Errorf("subid-source:dynamic: %w", err)
	}

	uByUsername, uByUsernameErr := execGetsubids(getsubidsExe, false, username)
	uByUID, uByUIDErr := execGetsubids(getsubidsExe, false, strconv.Itoa(uid))
	// Typically, uByUsernameErr == nil, uByUIDErr == "Error fetching ranges" (exit code 1)
	if uByUsernameErr != nil {
		logrus.WithError(uByUsernameErr).Debugf("subid-source:dynamic: failed to get subuids by the username %q", username)
	}
	if uByUIDErr != nil {
		logrus.WithError(uByUIDErr).Debugf("subid-source:dynamic: failed to get subuids by the UID %d", uid)
		if uByUsernameErr != nil {
			return nil, nil, fmt.Errorf("subid-source:dynamic: failed to get subuids by the username %q: %w; also failed to get subuids by the UID %d: %v",
				username, uByUsernameErr, uid, uByUIDErr)
		}
	}

	gByUsername, gByUsernameErr := execGetsubids(getsubidsExe, true, username)
	gByUID, gByUIDErr := execGetsubids(getsubidsExe, true, strconv.Itoa(uid))
	// Typically, gByUsernameErr == nil, gByUIDErr == "Error fetching ranges" (exit code 1)
	if gByUsernameErr != nil {
		logrus.WithError(gByUsernameErr).Debugf("subid-source:dynamic: failed to get subgids by the username %q", username)
	}
	if gByUIDErr != nil {
		logrus.WithError(gByUIDErr).Debugf("subid-source:dynamic: failed to get subgids by the UID %d", uid)
		if gByUsernameErr != nil {
			return nil, nil, fmt.Errorf("subid-source:dynamic: failed to get subgids by the username %q: %w; also failed to get subuids by the UID %d: %v",
				username, gByUsernameErr, uid, gByUIDErr)
		}
	}

	u := withoutDuplicates(append(uByUsername, uByUID...))
	g := withoutDuplicates(append(gByUsername, gByUID...))
	return u, g, nil
}

// execGetsubids executes `getsubids [-g] user`
func execGetsubids(exe string, g bool, s string) ([]idtools.SubIDRange, error) {
	var args []string
	if g {
		args = append(args, "-g")
	}
	var stderr bytes.Buffer
	args = append(args, s)
	cmd := exec.Command(exe, args...)
	cmd.Stderr = &stderr
	logrus.Debugf("Executing %v", cmd.Args)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to exec %v: %w (stdout=%q, stderr=%q)", cmd.Args, err, string(out), stderr.String())
	}
	r := bytes.NewReader(out)
	ranges, warns, err := parseGetsubidsOutput(r)
	for _, warn := range warns {
		logrus.Warnf("Error while parsing the result of %v: %s (stdout=%q, stderr=%q)", cmd.Args, warn, string(out), stderr.String())
	}
	return ranges, err
}

func parseGetsubidsOutput(r io.Reader) (res []idtools.SubIDRange, warns []string, err error) {
	sc := bufio.NewScanner(r)
	for i := 0; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		// line is like "0: foo 100000 655360"
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		splitByColon := strings.Split(line, ":")
		switch len(splitByColon) {
		case 0, 1:
			return res, warns, fmt.Errorf("line %d: unparsable line %q", i+1, line)
		case 2:
			// NOP
		default:
			warns = append(warns, fmt.Sprintf("line %d: line %q contains unknown fields", i+1, line))
		}
		triplet := strings.Fields(strings.TrimSpace(splitByColon[1]))
		switch len(triplet) {
		case 0, 1, 2:
			return res, warns, fmt.Errorf("line %d: unparsable line %q", i+1, line)
		case 3:
			// NOP
		default:
			warns = append(warns, fmt.Sprintf("line %d: line %q contains unknown fields", i+1, line))
		}
		var entry idtools.SubIDRange
		entry.Start, err = strconv.Atoi(triplet[1])
		if err != nil {
			return res, warns, fmt.Errorf("line %d: unparsable line %q: failed to Atoi(%q): %w", i+1, line, triplet[1], err)
		}
		entry.Length, err = strconv.Atoi(triplet[2])
		if err != nil {
			return res, warns, fmt.Errorf("line %d: unparsable line %q: failed to Atoi(%q): %w", i+1, line, triplet[2], err)
		}
		res = append(res, entry)
	}
	err = sc.Err()
	return
}
