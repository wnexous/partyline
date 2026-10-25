package whatsapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/connectors"
)

// REACT-01: a group reaction goes to /message/react with the instance token, the target id,
// the emoji, fromMe false and the target's sender as participant.
func TestReactInGroupNamesParticipant(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", TargetSenderID: peerJID, Emoji: "👍",
	})
	require.NoError(t, err)
	req := f.next("/message/react")
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, acmeToken, req.APIKey)
	require.Equal(t, userAgent, req.UserAgent)
	require.Equal(t, map[string]any{
		"number": groupJID, "id": "ORIG1", "reaction": "👍", "fromMe": false, "participant": peerJID,
	}, req.Body)
}

// REACT-01: a DM reaction carries no participant.
func TestReactInDMHasNoParticipant(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, peerJID), TargetID: "ORIG2", TargetSenderID: peerJID, Emoji: "❤️",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"number": peerJID, "id": "ORIG2", "reaction": "❤️", "fromMe": false}, f.next("/message/react").Body)
}

// A non-2xx answer fails the reaction without the token in the error.
func TestReactFailure(t *testing.T) {
	f := newFakeEvolution(t)
	f.sendErr = http.StatusInternalServerError
	c := newConnector(t, f)
	err := c.React(context.Background(), connectors.Reaction{ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", TargetSenderID: peerJID, Emoji: "👍"})
	require.ErrorContains(t, err, "500")
	require.NotContains(t, err.Error(), acmeToken)
}

// CID-33: a reaction to a message the account itself wrote (typed on its phone) says so: fromMe is true,
// and in a group no participant is named, since the account is the one who wrote it.
func TestReactToAMessageTheAccountWroteIsFromMe(t *testing.T) {
	f := newFakeEvolution(t)
	c := newConnector(t, f)
	require.NoError(t, c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, groupJID), TargetID: "OWN1", TargetSenderID: peerJID, TargetFromMe: true, Emoji: "👍",
	}))
	require.Equal(t, map[string]any{"number": groupJID, "id": "OWN1", "reaction": "👍", "fromMe": true}, f.next("/message/react").Body)

	require.NoError(t, c.React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, peerJID), TargetID: "OWN2", TargetFromMe: true, Emoji: "❤️",
	}))
	require.Equal(t, map[string]any{"number": peerJID, "id": "OWN2", "reaction": "❤️", "fromMe": true}, f.next("/message/react").Body)
}

// CID-14, CID-33: evolution-go answers a reaction with the message it created, like any other send. The
// connector tells its id to Created, so that the echo of the reaction, which WhatsApp reports as a message of
// the account, is recognized.
func TestReactTellsTheIDOfTheReaction(t *testing.T) {
	f := newFakeEvolution(t)
	var ids []string
	require.NoError(t, newConnector(t, f).React(context.Background(), connectors.Reaction{
		ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", TargetSenderID: peerJID, Emoji: "👍",
		Created: func(id string) { ids = append(ids, id) },
	}))
	require.Equal(t, []string{"SENT-REACTION"}, ids)
}

// CID-33: the id is a nicety: an answer that is empty, not JSON or not what is expected does not turn a
// reaction that evolution-go took into a failure; Created is simply not told anything.
func TestReactSucceedsWhateverTheAnswerHolds(t *testing.T) {
	for name, answer := range map[string]string{
		"empty": "", "not JSON": "ok", "other JSON": `{"data":"done"}`, "no id": `{"data":{"Info":{}},"message":"success"}`,
		"a number as the id": `{"data":{"Info":{"ID":42}}}`, "null": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /instance/all", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"data":[{"name":"acme","token":"` + acmeToken + `","websocketEnable":"enabled","connected":true}]}`))
			})
			mux.HandleFunc("POST /message/react", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(answer)) })
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			c, err := New(connectors.Config{"base_url": srv.URL, "api_key": globalKey, "instances": []string{"acme"}},
				connectors.Deps{AllowPrivateNetworks: true, UserAgent: userAgent})
			require.NoError(t, err)
			told := false
			require.NoError(t, c.React(context.Background(), connectors.Reaction{
				ChatID: ChatID(testInstance, groupJID), TargetID: "ORIG1", Emoji: "👍", Created: func(string) { told = true },
			}))
			require.False(t, told)
		})
	}
}
