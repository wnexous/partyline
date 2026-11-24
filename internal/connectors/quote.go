package connectors

import (
	"strings"
	"unicode/utf8"

	"github.com/wnexous/partyline/internal/api/dto"
)

// quoteEllipsis ends a preview that was cut to dto.MaxQuotedPreview.
const quoteEllipsis = "…"

// QuotePreview is the preview of a quoted message (QC-02), from what the platform event carries of
// it: its text, or for media a bracketed kind followed by its caption when there is one ("[image]",
// "[image] the menu", "[document: plan.pdf]"). It is one line, at most dto.MaxQuotedPreview
// characters, cut with "…". kind is a dto.MessageKind value (empty is text); name is the file name
// of a document. A message of no known kind and without text has no preview.
func QuotePreview(kind dto.MessageKind, text, name string) string {
	text = oneLine(text)
	tag := ""
	switch kind {
	case dto.MessageKindImage, dto.MessageKindVideo, dto.MessageKindAudio, dto.MessageKindSticker,
		dto.MessageKindLocation, dto.MessageKindContact, dto.MessageKindPoll:
		tag = "[" + string(kind) + "]"
	case dto.MessageKindDocument:
		tag = "[document]"
		if name = oneLine(name); name != "" {
			tag = "[document: " + name + "]"
		}
	}
	preview := tag
	switch {
	case tag == "":
		preview = text
	case text != "":
		preview = tag + " " + text
	}
	if utf8.RuneCountInString(preview) <= dto.MaxQuotedPreview {
		return preview
	}
	keep := dto.MaxQuotedPreview - utf8.RuneCountInString(quoteEllipsis)
	return strings.TrimRight(string([]rune(preview)[:keep]), " ") + quoteEllipsis
}

// oneLine joins the words of s with single spaces: a preview is an attribute of one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
