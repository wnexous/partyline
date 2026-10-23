package agents

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/api/httpx"
)

func headers(kv ...string) http.Header {
	h := http.Header{}
	h.Set(dto.HeaderFolder, url.PathEscape("/work/acme"))
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// AD-020: the three placement headers are optional; --org and --parent are plain text, --position is
// percent-encoded like the folder so a name with spaces or accents survives a header.
func TestFolderIDFromHeaderReadsThePlacementHeaders(t *testing.T) {
	id, err := FolderIDFromHeader(headers(
		dto.HeaderOrg, "  acme ",
		dto.HeaderPosition, url.PathEscape("Líder de equipe"),
		dto.HeaderParent, " @lead ",
	))
	require.NoError(t, err)
	require.Equal(t, "acme", id.Org)
	require.Equal(t, "Líder de equipe", id.Position)
	require.Equal(t, "@lead", id.Parent)
	require.Equal(t, "/work/acme", id.Folder)
}

// A v0.5 CLI sends none of them, and a blank one is none: the defaults apply (AD-020).
func TestFolderIDFromHeaderWithoutPlacementHeadersReadsEmpty(t *testing.T) {
	for name, h := range map[string]http.Header{
		"absent": headers(),
		"blank":  headers(dto.HeaderOrg, " ", dto.HeaderPosition, "%20", dto.HeaderParent, "\t"),
	} {
		t.Run(name, func(t *testing.T) {
			id, err := FolderIDFromHeader(h)
			require.NoError(t, err)
			require.Empty(t, id.Org)
			require.Empty(t, id.Position)
			require.Empty(t, id.Parent)
		})
	}
}

// A position that is not valid percent-encoding is a position no organization has: 422 position_unknown on
// the field, never a silent default (a plain "100%" must have been sent as "100%25").
func TestFolderIDFromHeaderRefusesAPositionThatIsNotPercentEncoded(t *testing.T) {
	_, err := FolderIDFromHeader(headers(dto.HeaderPosition, "100%"))
	var he *httpx.Error
	require.True(t, errors.As(err, &he), "got %v", err)
	require.Equal(t, http.StatusUnprocessableEntity, he.Status)
	require.Equal(t, dto.ErrorCodePositionUnknown, he.Code)
	require.Contains(t, he.Fields, "position")
}
