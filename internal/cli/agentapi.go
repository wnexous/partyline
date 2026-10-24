package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// getFromAgentAPI reads GET /api/v1/agent/<path> as the user of creds' agent token and decodes the JSON it
// answers into T, giving up after agentsTimeout (or earlier, if ctx does). A refusal carries the server's
// own message. It is what `partyline agents` and `partyline orgs` read their lists with.
func getFromAgentAPI[T any](ctx context.Context, creds cliconfig.Config, path string) (T, error) {
	var out T
	ctx, cancel := context.WithTimeout(ctx, agentsTimeout)
	defer cancel()
	u := strings.TrimRight(creds.URL, "/") + "/api/v1/agent/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+creds.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("cannot reach %s", creds.URL)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var body dto.ErrorBody
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error.Message != "" {
			return out, fmt.Errorf("the server answered %d: %s", resp.StatusCode, body.Error.Message)
		}
		return out, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("unexpected answer: %w", err)
	}
	return out, nil
}
