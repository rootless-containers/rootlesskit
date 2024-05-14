package child

import "strings"

func generateResolvConf(dns []string) []byte {
	var sb strings.Builder

	for _, nameserver := range dns {
		sb.WriteString("nameserver " + nameserver + "\n")
	}
	return []byte(sb.String())
}
