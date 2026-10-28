// Framing cases every adapter must render the same way (PUSH-06): a message record (the
// channel meta fields plus content, as `partyline listen` prints them) and the exact text the
// session receives.

import cases from "./framing.json";

export type FramingCase = { name: string; record: Record<string, string>; text: string };

// The cases live in framing.json so the Go side (channel.Frame) checks the same texts.
export const framingCases: FramingCase[] = cases;

// meta splits a record into the {content, meta} params of a channel notification.
export function notificationParams(record: Record<string, string>): { content: string; meta: Record<string, string> } {
	const { content, ...meta } = record;
	return { content: content ?? "", meta };
}

// The connection notices (SES-04, SES-05): `partyline listen` prints one as a JSON line, the
// channel pushes the same as a notification. They tell the person why Partyline is not
// connected here, so the adapters show them and never hand them to the model as a room message.
export const notices: Array<{ reason: string; record: Record<string, string> }> = [
	{
		reason: "taken_over",
		record: {
			type: "notice",
			reason: "taken_over",
			content: "This intelligence was opened in another session; Partyline is disconnected here.",
			author: "Partyline",
			author_kind: "system",
			sender_name: "",
			trusted: "false",
			kind: "system",
		},
	},
	{
		reason: "busy",
		record: {
			type: "notice",
			reason: "busy",
			content:
				'Builder is open in another session (OpenCode, since 09:30). Partyline is not connected here. To take over, restart with "partyline pi --take-over" or set PARTYLINE_TAKEOVER=1.',
			author: "Partyline",
			author_kind: "system",
			sender_name: "",
			trusted: "false",
			kind: "system",
		},
	},
];
