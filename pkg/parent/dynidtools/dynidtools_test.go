package dynidtools

import (
	"strings"
	"testing"

	"github.com/rootless-containers/rootlesskit/pkg/parent/idtools"
	"gotest.tools/v3/assert"
)

func TestParseGetsubidsOutput(t *testing.T) {
	const s = `# foo
0: foo 100000 655360
`
	expected := []idtools.SubIDRange{
		{
			Start:  100000,
			Length: 655360,
		},
	}
	got, warn, err := parseGetsubidsOutput(strings.NewReader(s))
	assert.NilError(t, err)
	assert.Equal(t, 0, len(warn))
	assert.DeepEqual(t, expected, got)
}
