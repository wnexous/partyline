import type { MemberSettingsForm } from "@/lib/schemas";
import {
  DeliveryModeMentions,
  DeliveryModeTrusted,
  type RoomMember,
  type UpdateMemberRequest,
} from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";

/** The short, non-default settings of an agent member, for the member row. */
export function memberSettingsSummary(m: RoomMember): MessageKey[] {
  const out: MessageKey[] = [];
  if (m.delivery === DeliveryModeMentions) out.push("memberSettings.mentionsOnly");
  if (m.delivery === DeliveryModeTrusted) out.push("memberSettings.trustedOnly");
  return out;
}

/** What the form changed of a member's settings: only the fields that differ are sent. */
export function memberChanges(m: RoomMember, v: MemberSettingsForm): UpdateMemberRequest {
  const req: UpdateMemberRequest = {};
  const name = v.display_name.trim();
  if (name !== m.display_name) req.display_name = name;
  if (v.delivery !== m.delivery) req.delivery = v.delivery;
  if (v.keywords.join("\n") !== m.keywords.join("\n")) req.keywords = v.keywords;
  return req;
}
