package connectors

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Signed attachment links expire after 10 minutes and cannot be forged.
func TestFileLinksExpire(t *testing.T) {
	now := time.Unix(1792497600, 0)
	base, _ := url.Parse("https://partyline.example.test/")
	l := NewFileLinks(testBox(t), nil, base, func() time.Time { return now })
	link := l.URL("att-1")
	require.True(t, strings.HasPrefix(link, "https://partyline.example.test"+FileLinkPath), link)
	token := strings.TrimPrefix(link, "https://partyline.example.test"+FileLinkPath)

	now = now.Add(FileLinkTTL + time.Second)
	_, err := l.Resolve(context.Background(), token)
	require.ErrorIs(t, err, ErrLinkInvalid, "expired")
	_, err = l.Resolve(context.Background(), "not-a-token")
	require.ErrorIs(t, err, ErrLinkInvalid, "forged")
}
