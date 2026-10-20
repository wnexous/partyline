// Package scripts embeds the shell scripts the server serves.
package scripts

import (
	_ "embed"
	"strings"
)

//go:embed install.sh
var installScript string

// urlPlaceholder is the default instance URL line of install.sh before it is served.
const urlPlaceholder = "DEFAULT_URL='__PARTYLINE_URL__'"

// Install returns install.sh with instanceURL as the default URL for `partyline setup`
// (ONB-03). The URL is single-quoted for the shell.
func Install(instanceURL string) string {
	quoted := "'" + strings.ReplaceAll(instanceURL, "'", `'\''`) + "'"
	return strings.Replace(installScript, urlPlaceholder, "DEFAULT_URL="+quoted, 1)
}
