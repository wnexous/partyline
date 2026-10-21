package intelligences

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ai(folder string, online bool) AIContact {
	return AIContact{ID: "id:" + folder, Folder: folder, Online: online}
}

// TREE-01, TREE-02 and every example of the design's tree section.
func TestBuildTreeFolders(t *testing.T) {
	const home = "/home/u"

	cases := []struct {
		name string
		home string
		ais  []AIContact
		want []TreeNode
	}{
		{
			name: "identities nest and chains collapse under home",
			home: home,
			ais:  []AIContact{ai("/home/u/a", false), ai("/home/u/a/b", false), ai("/home/u/c/d/e", false)},
			want: []TreeNode{
				{AI: ai("/home/u/a", false), Name: "a", Children: []TreeNode{
					{AI: ai("/home/u/a/b", false), Name: "b"},
				}},
				{AI: ai("/home/u/c/d/e", false), Path: "c/d", Name: "e"},
			},
		},
		{
			name: "chains without identities collapse to one path label",
			home: home,
			ais:  []AIContact{ai("/home/u/projects/acme/agents/discord", false)},
			want: []TreeNode{
				{AI: ai("/home/u/projects/acme/agents/discord", false), Path: "projects/acme/agents", Name: "discord"},
			},
		},
		{
			name: "home folder renders as tilde",
			home: home,
			ais:  []AIContact{ai("/home/u", false), ai("/home/u/x", false)},
			want: []TreeNode{
				{AI: ai("/home/u", false), Name: "~", Children: []TreeNode{
					{AI: ai("/home/u/x", false), Name: "x"},
				}},
			},
		},
		{
			name: "outside home is absolute",
			home: home,
			ais:  []AIContact{ai("/srv/api", false), ai("/srv/api/web/ui", false)},
			want: []TreeNode{
				{AI: ai("/srv/api", false), Path: "/srv", Name: "api", Children: []TreeNode{
					{AI: ai("/srv/api/web/ui", false), Path: "web", Name: "ui"},
				}},
			},
		},
		{
			name: "outside home without an identity ancestor",
			home: home,
			ais:  []AIContact{ai("/srv/api", false)},
			want: []TreeNode{
				{AI: ai("/srv/api", false), Path: "/srv", Name: "api"},
			},
		},
		{
			name: "root folder stays root",
			home: home,
			ais:  []AIContact{ai("/", false)},
			want: []TreeNode{
				{AI: ai("/", false), Name: "/"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildTree(tc.home, tc.ais)
			require.Equal(t, tc.want, got)
		})
	}
}

// TREE-03: siblings sort by Path and Name, case-insensitive.
func TestBuildTreeSiblingOrder(t *testing.T) {
	const home = "/home/u"
	got := BuildTree(home, []AIContact{
		ai("/home/u/Zed", false),
		ai("/home/u/apple", false),
		ai("/home/u/c/d/e", false),
		ai("/home/u/Banana", false),
	})
	require.Equal(t, []string{"apple", "Banana", "e", "Zed"}, nodeNames(got))

	nested := BuildTree(home, []AIContact{
		ai("/home/u/a", false),
		ai("/home/u/a/z", false),
		ai("/home/u/a/B", false),
		ai("/home/u/a/c/d", false),
	})
	require.Len(t, nested, 1)
	require.Equal(t, []string{"B", "d", "z"}, nodeNames(nested[0].Children))

	// The sort key is the rendered "Path › Name", not the last segment alone.
	byPath := BuildTree(home, []AIContact{
		ai("/home/u/b", false),
		ai("/home/u/a/z", false),
	})
	require.Equal(t, []string{"z", "b"}, nodeNames(byPath))
}

// TREE-03: machines with an online AI sort first, then by label (falling back to the
// hostname), case-insensitively.
func TestBuildMachinesOrder(t *testing.T) {
	const home = "/home/u"
	got := BuildMachines([]MachineInput{
		{Hostname: "zulu-host", Label: "Zulu", Home: home, AIs: []AIContact{ai("/home/u/zulu", true)}},
		{Hostname: "beta-host", Label: "Beta", Home: home, AIs: []AIContact{ai("/home/u/beta", false)}},
		{Hostname: "gamma", Home: home, AIs: []AIContact{ai("/home/u/gamma", false)}},
		{Hostname: "alpha-host", Label: "alpha", Home: home, AIs: []AIContact{ai("/home/u/alpha", true)}},
		{Hostname: "delta", Home: home, AIs: []AIContact{ai("/home/u/delta", false)}},
	})

	require.Equal(t, []string{"alpha", "Zulu", "Beta", "delta", "gamma"}, machineLabels(got))
	require.Equal(t, []bool{true, true, false, false, false}, machineOnline(got))
}

// TREE-01 independent test: the same folders on two machines render as two machine groups
// with the same shape.
func TestBuildMachinesTwoMachines(t *testing.T) {
	folders := func(home string) []AIContact {
		return []AIContact{ai(home+"/a", false), ai(home+"/a/b", false), ai(home+"/c/d/e", false)}
	}
	got := BuildMachines([]MachineInput{
		{Hostname: "devbox", Home: "/home/u", AIs: folders("/home/u")},
		{Hostname: "notebook", Home: "/home/n", AIs: folders("/home/n")},
	})

	require.Len(t, got, 2)
	for _, m := range got {
		require.Equal(t, []string{"a", "e"}, nodeNames(m.Nodes))
		require.Equal(t, []string{"b"}, nodeNames(m.Nodes[0].Children))
		require.Equal(t, "c/d", m.Nodes[1].Path)
	}
}

// Two identities at the same folder depth on different machines stay on their own machine,
// with the same displayed folder.
func TestBuildMachinesSameFolderDepth(t *testing.T) {
	got := BuildMachines([]MachineInput{
		{Hostname: "m1", Home: "/home/u", AIs: []AIContact{ai("/home/u/a", false)}},
		{Hostname: "m2", Home: "/srv", AIs: []AIContact{ai("/srv/a", false)}},
	})

	require.Len(t, got, 2)
	for _, m := range got {
		require.Len(t, m.Nodes, 1)
		require.Equal(t, "a", m.Nodes[0].Name)
		require.Empty(t, m.Nodes[0].Path)
		require.Equal(t, m.Hostname, m.Nodes[0].AI.Machine)
	}
}

// Output is deterministic regardless of input order.
func TestBuildTreeDeterministic(t *testing.T) {
	const home = "/home/u"
	ais := []AIContact{
		ai("/home/u/a", false),
		ai("/home/u/a/b", false),
		ai("/home/u/c/d/e", false),
	}
	reversed := []AIContact{ais[2], ais[1], ais[0]}
	require.Equal(t, BuildTree(home, ais), BuildTree(home, reversed))

	machines := []MachineInput{
		{Hostname: "m1", Home: home, AIs: ais},
		{Hostname: "m2", Home: home, AIs: ais},
	}
	reversedMachines := []MachineInput{machines[1], machines[0]}
	require.Equal(t, BuildMachines(machines), BuildMachines(reversedMachines))
}

func nodeNames(nodes []TreeNode) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func machineLabels(machines []MachineTree) []string {
	labels := make([]string, len(machines))
	for i, m := range machines {
		labels[i] = m.Label
	}
	return labels
}

func machineOnline(machines []MachineTree) []bool {
	online := make([]bool, len(machines))
	for i, m := range machines {
		online[i] = m.Online
	}
	return online
}
