import { useId } from "react";
import { useMe } from "@/entities/account";
import { LocaleChoice } from "@/features/change-language";
import { PasswordSection } from "@/features/change-password";
import { DeleteAccountSection } from "@/features/delete-account";
import { ProfileSection } from "@/features/edit-profile";
import { ServersSection } from "@/features/manage-runners";
import { SecretsSection } from "@/features/manage-secrets";
import { NotificationsSection } from "@/features/push-notifications";
import { SessionsSection } from "@/features/revoke-session";
import { DefaultOrgSection } from "@/features/set-default-org";
import { ThemeChoice } from "@/features/toggle-theme";
import type { Me } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PageHeader } from "@/shared/ui/molecules/page-header";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";

/** The account settings: profile, appearance, notifications and the rest, one section each. */
export function AccountPage({ initialMe }: { initialMe: Me }) {
  const t = useT();
  const me = useMe().data ?? initialMe;
  return (
    <>
      <PageHeader title={t("page.accountTitle")} description={t("page.accountBody")} />
      <div className="max-w-2xl divide-y px-4 md:px-8">
        <ProfileSection me={me} />
        <AppearanceSection />
        <NotificationsSection me={me} />
        <DefaultOrgSection />
        <PasswordSection />
        <SessionsSection />
        <ServersSection />
        <SecretsSection />
        <DeleteAccountSection />
      </div>
    </>
  );
}

/** The look of this browser (the color scheme) and the interface language. */
function AppearanceSection() {
  const t = useT();
  const id = useId();
  return (
    <Section
      id={`${id}-look`}
      title={t("account.appearance")}
      description={t("account.appearanceBody")}
    >
      <div className="space-y-6">
        <ThemeChoice idBase={`${id}-theme`} />
        <LocaleChoice idBase={`${id}-locale`} />
      </div>
    </Section>
  );
}
