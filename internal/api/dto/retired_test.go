package dto_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CID-22 (T35): the contract has no chat field left. The generated TypeScript types come from these
// declarations, so a retired field kept here would reach the web app, and a field that is never filled
// (a message's outbox is always empty now) cannot be seen in any response. The scan reads every
// declaration of the package, so a type added later is covered as well.
func TestContractDeclaresNoRetiredChatField(t *testing.T) {
	retiredJSON := map[string]string{
		"bridge_chats":       "Room, CreateRoomRequest, UpdateRoomRequest",
		"broadcast":          "Room, UpdateRoomRequest",
		"catch_all":          "Room",
		"outbound":           "RoomMember, AddMemberRequest, UpdateMemberRequest",
		"sign_outbound":      "RoomMember, UpdateMemberRequest",
		"outbox":             "Message",
		"notify_chat_id":     "Me",
		"auto_route_scope":   "Connector, UpdateConnectorRequest",
		"auto_route_room_id": "Connector, UpdateConnectorRequest",
	}
	retiredPrefixes := []string{
		"AutoRoute",            // the catch-all scope and the status details routing set
		"Outbox", "LiveOutbox", // the delivery of a message to a chat and its live frame
		"LiveEventTypeOutbox",
		"NotifyOption", "SetNotify", // the app a user chose for copies of AI DMs
		"MaxMessageRecipients", // the cap of chats one post could name
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var found []string
	parsed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, entry.Name(), nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		parsed++
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Field: // struct fields (and interface methods, which have no tag)
				if x.Tag == nil {
					return true
				}
				tag := reflect.StructTag(strings.Trim(x.Tag.Value, "`"))
				if jsonName, _, _ := strings.Cut(tag.Get("json"), ","); retiredJSON[jsonName] != "" {
					found = append(found, fmt.Sprintf("%s: the json field %q (it was in %s)", fset.Position(x.Pos()), jsonName, retiredJSON[jsonName]))
				}
			case *ast.TypeSpec:
				found = append(found, retiredDeclarations(fset, []*ast.Ident{x.Name}, retiredPrefixes)...)
			case *ast.ValueSpec:
				found = append(found, retiredDeclarations(fset, x.Names, retiredPrefixes)...)
			}
			return true
		})
	}
	require.Greater(t, parsed, 5, "the dto package was not parsed")
	require.Empty(t, found, "the contract still declares retired chat fields")
}

// retiredDeclarations lists the identifiers that start with a prefix of a retired feature.
func retiredDeclarations(fset *token.FileSet, ids []*ast.Ident, prefixes []string) []string {
	var found []string
	for _, id := range ids {
		for _, p := range prefixes {
			if strings.HasPrefix(id.Name, p) {
				found = append(found, fmt.Sprintf("%s: %s (%s... belongs to a retired chat feature)", fset.Position(id.Pos()), id.Name, p))
			}
		}
	}
	return found
}
