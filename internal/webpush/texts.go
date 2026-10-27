package webpush

import "strings"

// The texts the server itself writes into a push payload follow the recipient's account language.
// A message notification is built from the conversation, so only the test notification and the
// connection alert carry a language (PUSH-15, ALERT-07, ALERT-08).
const (
	// testTitle is the test notification's title in every language: it is the product's name.
	testTitle = "partyline"
	// ptLanguage is the base language of the Portuguese table: pt-BR and pt-PT both read it.
	ptLanguage = "pt"
)

// texts is the fixed text of the server-written notifications in one language.
type texts struct {
	// testBody is the test notification's preview (PUSH-15).
	testBody string
	// alertTitle starts the connection alert's title; the connection's name completes it
	// (ALERT-07).
	alertTitle string
	// The four fixed reason sentences; the platform's own detail is never sent (ALERT-08).
	reasonWhatsAppDisconnected string
	reasonLoginExpired         string
	reasonNoOperator           string
	reasonServiceUnreachable   string
}

// englishTexts holds the texts of the accounts with no locale and of every other language.
var englishTexts = texts{
	testBody:                   "Notifications are on",
	alertTitle:                 "Connection down: ",
	reasonWhatsAppDisconnected: "The WhatsApp number was disconnected",
	reasonLoginExpired:         "Login expired",
	reasonNoOperator:           "The connection has no AI operating it",
	reasonServiceUnreachable:   "Cannot reach the service",
}

// portugueseTexts holds the texts of the Portuguese accounts.
var portugueseTexts = texts{
	testBody:                   "As notificações estão ligadas",
	alertTitle:                 "Conexão caiu: ",
	reasonWhatsAppDisconnected: "O número do WhatsApp foi desconectado",
	reasonLoginExpired:         "O login expirou",
	reasonNoOperator:           "A conexão não tem uma IA operando",
	reasonServiceUnreachable:   "Não foi possível falar com o serviço",
}

// textsFor returns the texts of locale: its base language decides, so pt-BR and pt-PT read the
// Portuguese table; an empty or unsupported locale reads English.
func textsFor(locale string) texts {
	base, _, _ := strings.Cut(strings.ToLower(locale), "-")
	if base == ptLanguage {
		return portugueseTexts
	}
	return englishTexts
}
