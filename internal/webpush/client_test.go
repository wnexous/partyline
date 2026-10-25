package webpush_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/webpush"
)

func testSubscription(endpoint string) webpush.Subscription {
	return webpush.Subscription{
		ID:       "sub_test",
		Endpoint: endpoint,
		P256dh:   rfcUAKey,
		Auth:     rfcAuth,
	}
}

func TestClientSendsTheRFC8291Request(t *testing.T) {
	type recorded struct {
		method        string
		encoding      string
		contentType   string
		ttl           string
		urgency       string
		authorization string
		body          []byte
	}
	var got recorded

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.encoding = r.Header.Get("Content-Encoding")
		got.contentType = r.Header.Get("Content-Type")
		got.ttl = r.Header.Get("TTL")
		got.urgency = r.Header.Get("Urgency")
		got.authorization = r.Header.Get("Authorization")
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	vapid := testVAPID(t)
	client := webpush.NewClient(vapid, testSubject)
	client.Doer = server.Client()

	status, err := client.Send(context.Background(), testSubscription(server.URL+"/send/capability"), []byte(`{"title":"Design"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "aes128gcm", got.encoding)
	require.Equal(t, "application/octet-stream", got.contentType)
	require.Equal(t, "3600", got.ttl)
	require.Equal(t, "normal", got.urgency)
	require.True(t, strings.HasPrefix(got.authorization, "vapid t="))
	require.Contains(t, got.authorization, ",k="+vapid.Public)
	require.NotEmpty(t, got.body)
}

func TestClientReturnsThePushServiceStatus(t *testing.T) {
	vapid := testVAPID(t)

	for _, code := range []int{http.StatusNotFound, http.StatusGone, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		client := webpush.NewClient(vapid, testSubject)
		client.Doer = server.Client()

		status, err := client.Send(context.Background(), testSubscription(server.URL+"/send/capability"), []byte(`{}`))
		server.Close()
		require.NoError(t, err)
		require.Equal(t, code, status)
	}

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	client := webpush.NewClient(vapid, testSubject)
	client.Doer = closed.Client()
	_, err := client.Send(context.Background(), testSubscription(closed.URL+"/send/capability"), []byte(`{}`))
	require.Error(t, err)
}
