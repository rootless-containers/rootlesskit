package main

import (
	"fmt"
	"sort"

	"github.com/urfave/cli/v2"
)

const (
	CategoryDebug       = "Debug"
	CategoryState       = "State"
	CategoryNetwork     = "Network"
	CategorySlirp4netns = "Network [slirp4netns]"
	CategoryVPNKit      = "Network [vpnkit]"
	CategoryLXCUserNic  = "Network [lxc-user-nic]"
	CategoryPort        = "Port"
	CategoryMount       = "Mount"
	CategoryProcess     = "Process"
)

type CategorizedFlag interface {
	cli.Flag
	Category() string
}

func Categorize(f cli.Flag, category string) CategorizedFlag {
	return &flag{
		Flag:     f,
		category: category,
	}
}

type flag struct {
	cli.Flag
	category string
}

func (f *flag) Category() string {
	return f.category
}

func formatFlags(flags []cli.Flag) string {
	var res string
	m := make(map[string][]cli.Flag)
	for _, f := range flags {
		cat := "(Uncategorized)"
		if x, ok := f.(CategorizedFlag); ok {
			if cat2 := x.Category(); cat2 != "" {
				cat = cat2
			}
		}
		if _, ok := m[cat]; !ok {
			m[cat] = make([]cli.Flag, 0)
		}
		m[cat] = append(m[cat], f)
	}

	var catList []string
	for c := range m {
		catList = append(catList, c)
	}
	sort.Strings(catList)

	for _, cat := range catList {
		catFlags, ok := m[cat]
		if !ok {
			continue
		}
		res += fmt.Sprintf("  %s:\t\n", cat)
		for _, f := range catFlags {
			res += fmt.Sprintf("    %s\n", f.String())
		}
		res += "  \t\n"
	}
	return res
}
