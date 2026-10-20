package api

import (
	"io"
	"net/http"

	"github.com/wnexous/partyline/scripts"
)

// installScript serves the installer at GET /install.sh with this instance's URL as the
// default for `partyline setup` (ONB-03): curl -fsSL <instance>/install.sh | sh.
func (s *server) installScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, scripts.Install(s.publicURL()))
}
