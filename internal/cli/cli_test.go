package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = Run(append([]string{"partyline"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionPrintsDefaultDev(t *testing.T) {
	code, out, _ := run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "dev\n", out)
}

func TestVersionPrintsLinkerValue(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v1.2.3"

	code, out, _ := run("version")
	require.Equal(t, 0, code)
	require.Equal(t, "v1.2.3\n", out)
}

func TestUnknownSubcommandExitsTwoWithUsage(t *testing.T) {
	code, out, errOut := run("bogus")
	require.Equal(t, 2, code)
	require.Empty(t, out)
	require.Contains(t, errOut, `unknown command "bogus"`)
	require.True(t, strings.Contains(errOut, "Usage: partyline <command>"), errOut)
}

func TestMissingSubcommandExitsTwoWithUsage(t *testing.T) {
	code, _, errOut := run()
	require.Equal(t, 2, code)
	require.Contains(t, errOut, "Usage: partyline <command>")
}
