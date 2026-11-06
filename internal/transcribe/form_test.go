package transcribe

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/store/db"
)

// filePart builds the body of a request for a stored file and returns its file part's header and
// content.
func filePart(t *testing.T, name, storedMime string) (filename, contentType string, content []byte) {
	t.Helper()
	audio := []byte("audio bytes")
	body, size, ct, err := form("model-x", db.NextPendingTranscriptionRow{Name: name, Mime: storedMime}, bytes.NewReader(audio), int64(len(audio)))
	require.NoError(t, err)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, size, int64(len(raw)), "the Content-Length is the body's")
	_, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	r := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	fields := map[string]string{}
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(p)
		require.NoError(t, err)
		if p.FormName() == "file" {
			filename, contentType, content = p.FileName(), p.Header.Get("Content-Type"), data
			continue
		}
		fields[p.FormName()] = string(data)
	}
	require.Equal(t, map[string]string{"model": "model-x", "response_format": "json"}, fields)
	require.Equal(t, audio, content)
	return filename, contentType, content
}

// The type a platform sent reaches the part header only as a clean audio type.
func TestTheFilePartCarriesACleanAudioType(t *testing.T) {
	cases := map[string]string{
		"audio/ogg; codecs=opus":          "audio/ogg; codecs=opus",
		"AUDIO/MPEG":                      "audio/mpeg",
		"audio/ogg\r\nX-Injected: yes":    "application/octet-stream",
		"audio/ogg; codecs=\"opus\r\nX\"": "application/octet-stream",
		"video/mp4":                       "application/octet-stream",
		"not a type":                      "application/octet-stream",
	}
	for stored, want := range cases {
		_, got, _ := filePart(t, "voice.ogg", stored)
		require.Equal(t, want, got, "stored %q", stored)
	}
}

// A strict service tells the format by the extension: a name without one gets the type's.
func TestTheFilePartNameHasAnExtension(t *testing.T) {
	cases := []struct{ name, mime, want string }{
		{"voice-message-2026-10-24-140509.webm", "audio/webm;codecs=opus", "voice-message-2026-10-24-140509.webm"},
		{"PTT-20261105", "audio/ogg; codecs=opus", "PTT-20261105.ogg"},
		{"note", "audio/mp4", "note.m4a"},
		{"clip", "audio/mpeg", "clip.mp3"},
		{"take", "audio/x-wav", "take.wav"},
		{"", "audio/webm", "voice.webm"},
		{"bad\r\nname", "audio/ogg", "badname.ogg"},
		{"../etc/passwd", "audio/ogg", "..etcpasswd.ogg"},
		{"odd", "audio/x-unknown", "odd"},
		{"PTT-20261105.OPUS", "audio/ogg", "PTT-20261105.OPUS"},
		{"voice.message", "audio/ogg", "voice.message.ogg"},
	}
	for _, c := range cases {
		got, _, _ := filePart(t, c.name, c.mime)
		require.Equal(t, c.want, got, "stored %q, %q", c.name, c.mime)
	}
}
