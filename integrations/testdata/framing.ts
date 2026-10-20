// Framing cases every adapter must render the same way (PUSH-06): a message record (the
// channel meta fields plus content, as `partyline listen` prints them) and the exact text the
// session receives.

import cases from "./framing.json";

export type FramingCase = { name: string; record: Record<string, string>; text: string };

// The cases live in framing.json so the Go side (wait_for_messages, turn-end hooks) checks the
// same texts.
export const framingCases: FramingCase[] = cases;

// meta splits a record into the {content, meta} params of a channel notification.
export function notificationParams(record: Record<string, string>): { content: string; meta: Record<string, string> } {
	const { content, ...meta } = record;
	return { content: content ?? "", meta };
}
