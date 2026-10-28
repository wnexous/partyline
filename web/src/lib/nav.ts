import {
  Building2Icon,
  ContactRoundIcon,
  FingerprintIcon,
  KeyRoundIcon,
  type LucideIcon,
  MailPlusIcon,
  MessagesSquareIcon,
  MonitorSmartphoneIcon,
  ScrollTextIcon,
  SettingsIcon,
  UsersIcon,
} from "lucide-react";
import type { MessageKey } from "@/shared/i18n";

/*
 * Every destination outside a conversation (SET-03, SET-11): the account menu lists them by what
 * they configure and the palette finds them. The list lives outside the sidebar so the menu, the
 * palette and their tests share one truth without importing the messenger panel.
 */

/** A destination: what it is called, its icon, where it lives and whether only its home matches. */
export interface NavItem {
  to:
    | "/"
    | "/contacts"
    | "/orgs"
    | "/account"
    | "/identities"
    | "/tokens"
    | "/devices"
    | "/admin/users"
    | "/admin/invites"
    | "/admin/audit";
  label: MessageKey;
  icon: LucideIcon;
  exact?: boolean;
}

/** A menu group: its heading and the rows under it; the admin group is an instance admin's. */
export interface NavGroup {
  label: MessageKey;
  items: NavItem[];
  adminOnly?: boolean;
}

export const NAV_GROUPS: NavGroup[] = [
  {
    label: "nav.groupAccount",
    items: [
      { to: "/account", label: "nav.account", icon: SettingsIcon },
      { to: "/identities", label: "nav.identities", icon: FingerprintIcon },
      { to: "/tokens", label: "nav.tokens", icon: KeyRoundIcon },
      { to: "/devices", label: "nav.devices", icon: MonitorSmartphoneIcon },
    ],
  },
  {
    label: "nav.groupGoTo",
    items: [
      { to: "/", label: "nav.conversations", icon: MessagesSquareIcon, exact: true },
      { to: "/contacts", label: "nav.contacts", icon: ContactRoundIcon },
      { to: "/orgs", label: "nav.organizations", icon: Building2Icon },
    ],
  },
  {
    label: "nav.admin",
    adminOnly: true,
    items: [
      { to: "/admin/users", label: "nav.adminUsers", icon: UsersIcon },
      { to: "/admin/invites", label: "nav.adminInvites", icon: MailPlusIcon },
      { to: "/admin/audit", label: "nav.adminAudit", icon: ScrollTextIcon },
    ],
  },
];
