package harness

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type server struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

var ours = server{Command: "/opt/bin/partyline", Args: []string{"channel"}}

func edit(data string) *docEdit { return &docEdit{path: "cfg.json", data: []byte(data)} }

// HAR-02: adding a member to an indented object (tabs here) keeps every other byte and
// follows the file's indentation; removing it gives back the original bytes.
func TestSetMemberKeepsOtherBytes(t *testing.T) {
	orig := "{\n\t\"theme\": \"dark\",   \"x\":1e3,\n\t\"mcpServers\": {\n\t\t\"other\": {\"command\": \"x\", \"env\": {\"A\": \"\\u00e9\"}}\n\t}\n}\n"
	d := edit(orig)
	require.NoError(t, d.set(ours, "mcpServers", "partyline"))
	require.Equal(t, "{\n\t\"theme\": \"dark\",   \"x\":1e3,\n\t\"mcpServers\": {\n\t\t\"other\": {\"command\": \"x\", \"env\": {\"A\": \"\\u00e9\"}},\n"+
		"\t\t\"partyline\": {\n\t\t  \"command\": \"/opt/bin/partyline\",\n\t\t  \"args\": [\n\t\t    \"channel\"\n\t\t  ]\n\t\t}\n\t}\n}\n", string(d.data))

	ok, err := d.del("mcpServers", "partyline")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, orig, string(d.data))
}

// HAR-02: missing parent objects are created and pruned again on removal.
func TestSetCreatesAndDelPrunesParents(t *testing.T) {
	orig := "{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n"
	d := edit(orig)
	require.NoError(t, d.set(true, "mcp", "partyline", "enabled"))
	require.Equal(t, "{\n  \"$schema\": \"https://opencode.ai/config.json\",\n  \"mcp\": {\n    \"partyline\": {\n      \"enabled\": true\n    }\n  }\n}\n", string(d.data))
	ok, err := d.del("mcp", "partyline", "enabled")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, orig, string(d.data))
}

// A one-line file stays on one line.
func TestSetCompact(t *testing.T) {
	orig := `{"a":1,"mcpServers":{}}`
	d := edit(orig)
	require.NoError(t, d.set(ours, "mcpServers", "partyline"))
	require.Equal(t, `{"a":1,"mcpServers":{"partyline":{"command":"/opt/bin/partyline","args":["channel"]}}}`, string(d.data))
	_, err := d.del("mcpServers", "partyline")
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(d.data), "the emptied mcpServers object is pruned")
}

// An existing member is replaced in place; members before and after it are untouched.
func TestSetReplacesInPlace(t *testing.T) {
	d := edit("{\n  \"mcpServers\": {\n    \"partyline\": {\"command\": \"/old/partyline\"},\n    \"z\": {\"command\": \"z\"}\n  },\n  \"after\": [1, 2]\n}")
	require.NoError(t, d.set(ours, "mcpServers", "partyline"))
	require.Equal(t, "{\n  \"mcpServers\": {\n    \"partyline\": {\n      \"command\": \"/opt/bin/partyline\",\n      \"args\": [\n        \"channel\"\n      ]\n    },\n    \"z\": {\"command\": \"z\"}\n  },\n  \"after\": [1, 2]\n}", string(d.data))

	// Removing a member that is not the last one takes its separator with it.
	_, err := d.del("mcpServers", "partyline")
	require.NoError(t, err)
	require.Equal(t, "{\n  \"mcpServers\": {\n    \"z\": {\"command\": \"z\"}\n  },\n  \"after\": [1, 2]\n}", string(d.data))
}

// A missing or empty file becomes an indented object; removing the member empties it.
func TestSetOnEmptyDocument(t *testing.T) {
	d := edit("")
	require.NoError(t, d.set(ours, "mcpServers", "partyline"))
	require.Equal(t, "{\n  \"mcpServers\": {\n    \"partyline\": {\n      \"command\": \"/opt/bin/partyline\",\n      \"args\": [\n        \"channel\"\n      ]\n    }\n  }\n}\n", string(d.data))
	_, err := d.del("mcpServers", "partyline")
	require.NoError(t, err)
	require.True(t, d.empty())
}

// Edge case (HAR-02): a file that is not valid JSON is reported, not edited.
func TestInvalidJSONIsAParseError(t *testing.T) {
	for _, data := range []string{"{\n  \"a\": 1,\n}", "{\"a\": 1} // comment", "[1, 2]"} {
		d := edit(data)
		err := d.set(ours, "mcpServers", "partyline")
		var pe *ParseError
		require.True(t, errors.As(err, &pe), "data %q: %v", data, err)
		require.Equal(t, data, string(d.data))
	}
}

// A path through a value that is not an object is refused.
func TestSetThroughNonObject(t *testing.T) {
	d := edit(`{"mcp": [1]}`)
	require.ErrorContains(t, d.set(ours, "mcp", "partyline"), `"mcp" is not an object`)
}

// Hook arrays: appending keeps the other groups; removing gives back the original bytes,
// for the last element and for one in the middle.
func TestArrayAppendAndRemove(t *testing.T) {
	orig := "{\n  \"hooks\": {\n    \"Stop\": [\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"mine\"}]}\n    ]\n  }\n}\n"
	d := edit(orig)
	require.NoError(t, d.appendElem(map[string]int{"ours": 1}, "hooks", "Stop"))
	require.Equal(t, "{\n  \"hooks\": {\n    \"Stop\": [\n      {\"hooks\": [{\"type\": \"command\", \"command\": \"mine\"}]},\n      {\n        \"ours\": 1\n      }\n    ]\n  }\n}\n", string(d.data))
	n, err := d.removeElems(func(raw []byte) bool {
		return string(raw) != "{\"hooks\": [{\"type\": \"command\", \"command\": \"mine\"}]}"
	}, "hooks", "Stop")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, orig, string(d.data))

	d = edit(`{"Stop":[1,2,3]}`)
	n, err = d.removeElems(func(raw []byte) bool { return string(raw) == "2" }, "Stop")
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, `{"Stop":[1,3]}`, string(d.data))

	// The last element going away removes the array and the objects left empty.
	d = edit("{\n  \"model\": \"x\",\n  \"hooks\": {\n    \"AfterAgent\": [\n      {\"a\": 1}\n    ]\n  }\n}")
	_, err = d.removeElems(func([]byte) bool { return true }, "hooks", "AfterAgent")
	require.NoError(t, err)
	require.Equal(t, "{\n  \"model\": \"x\"\n}", string(d.data))
}

// A missing array is created with the element.
func TestAppendCreatesArray(t *testing.T) {
	d := edit("{\n  \"hooks\": {}\n}")
	require.NoError(t, d.appendElem(1, "hooks", "Stop"))
	require.Equal(t, "{\n  \"hooks\": {\n    \"Stop\": [\n      1\n    ]\n  }\n}", string(d.data))
}
