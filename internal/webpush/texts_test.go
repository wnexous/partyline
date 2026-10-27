package webpush

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// PUSH-15, ALERT-07, ALERT-08: the server-written texts follow the account's base language; an
// empty or unsupported locale reads English.
func TestTextsFor(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		want   texts
	}{
		{"pt-BR", "pt-BR", portugueseTexts},
		{"a regional Portuguese tag", "pt-PT", portugueseTexts},
		{"en", "en", englishTexts},
		{"an empty locale", "", englishTexts},
		{"an unsupported locale", "fr-FR", englishTexts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, textsFor(tt.locale))
		})
	}
}
