package archtest

import "sort"

// layer is where a package of internal/ belongs (AD-025).
type layer string

const (
	// core is the funnel: integrations -> per-AI queues -> AI sessions -> sends. It must work, and be
	// testable, without the messenger.
	core layer = "core"
	// app is the messenger built on top of the funnel: rooms, conversations and what delivers a room's
	// messages to its AIs.
	app layer = "app"
	// kernel is what both share: identities, organizations, access, events, files and the like.
	kernel layer = "kernel"
	// infra is the storage, the configuration, the wiring, the HTTP layer and the command line, which
	// may use everything.
	infra layer = "infra"
)

// packagesOf lists the packages of internal/ of each layer, without the "internal/" prefix. A package
// that is not listed fails TestTheLayerTableMatchesThePackages: a new package must be placed here, and placing it
// is the decision this test exists for.
var packagesOf = map[layer][]string{
	core: {
		"connectors", "connectors/connectortest", "connectors/discord", "connectors/safehttp",
		"connectors/telegram", "connectors/webhook", "connectors/whatsapp",
		"platform",               // the per-AI queue
		"agents",                 // the registry, presence and the session gateway
		"identities",             // chat accounts linked to users
		"channel", "channel/mcp", // the session's end of the funnel
	},
	app: {
		"rooms", "conversations",
		"delivery", // room -> AI rules
		"live", "webui", "webpush",
	},
	kernel: {
		"orgs", "access", "intelligences", "users", "authn", "audit", "events", "handles", "validation",
		"attachments", "avatars", "secrets", "api/dto", "api/httpx",
		"runners",      // the runner registry and the hub of their connections (CA-01)
		"runners/spec", // the runner protocol shared with the CLI (CA-09)
	},
	infra: {
		"store", "store/db", "config", "app", "api", "api/convert", "cli", "cliconfig", "harness", "devices",
		"testkit", "datalock", "memlimit", "archtest",
	},
}

// edge is an import of one package of internal/ by another, both without the "internal/" prefix.
type edge struct{ from, to string }

// allowed are the imports through which the core still reaches the app (AD-025). THIS LIST MAY ONLY
// SHRINK: never add an entry to make the test pass, move the code instead. An entry whose import is gone,
// or that no longer leads to the app, fails the test until it is removed (and maxAllowed lowered).
//
// All of it is the session gateway, which builds the room backlog of an AI, checks its room membership
// and puts messages on the wire in the shape of the HTTP API. The way out is a feed seam: the gateway
// loops over feeds (the queue of the platform messages is one) and the app registers the room feed at
// wiring time, so that no core package names a room.
var allowed = []edge{
	{"agents", "rooms"},       // GatewayDeps.Rooms: the room details of the backlog and the membership check
	{"agents", "delivery"},    // which room messages an AI gets, and with which facts (pending.go, gateway.go)
	{"agents", "api/convert"}, // the wire shape of a message, and convert imports rooms
}

// maxAllowed is how many entries allowed may have. It follows the list down and never goes up.
const maxAllowed = 3

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
