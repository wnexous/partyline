import { ConversationList } from "./conversation-list";
import { ConversationSearch } from "./conversation-search";

/**
 * The search box and the list of the messenger panel, for the widget that lays the panel out: the
 * panel owns the query, these draw from it. Spread into `MessengerPanel` / `AppSidebar`.
 */
export const conversationSlots = {
  renderSearch: ({ query, setQuery }: { query: string; setQuery: (query: string) => void }) => (
    <ConversationSearch value={query} onChange={setQuery} />
  ),
  renderList: ({ query, setQuery }: { query: string; setQuery: (query: string) => void }) => (
    <ConversationList query={query} onClearQuery={() => setQuery("")} />
  ),
};
