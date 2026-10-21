// Package intelligences holds AI profiles, their visibility and the machine › folder tree.
package intelligences

import (
	"path"
	"sort"
	"strings"
)

// AIContact is the tree's input for one AI: where it lives and whether it is online.
// The contacts service fills the remaining profile fields in its own DTO.
type AIContact struct {
	ID      string
	Name    string
	Handle  string
	Machine string
	Folder  string
	Online  bool
}

// TreeNode is one identity in a machine's folder tree. Path is the collapsed chain of
// folders between the parent identity (or the root) and the node, empty when there is
// none; Name is the folder's own label ("~" and "/" for the roots). The UI and CLI render
// "Path › Name" when Path is not empty, else Name.
type TreeNode struct {
	AI       AIContact
	Path     string
	Name     string
	Children []TreeNode
}

// MachineTree is one machine with its folder tree. Label falls back to Hostname, and
// Online is true when any of the machine's AIs is online (TREE-03).
type MachineTree struct {
	Hostname string
	Label    string
	Online   bool
	Nodes    []TreeNode
}

// MachineInput is one machine for BuildMachines: its home folder and the AIs that belong
// to it.
type MachineInput struct {
	Hostname string
	Label    string
	Home     string
	AIs      []AIContact
}

// BuildTree builds one machine's folder tree (TREE-01, TREE-02). Folders under home are
// shown relative to "~"; other folders are absolute and the root is "/". An identity is a
// child of its nearest ancestor identity, and folder chains without an identity collapse
// into Path. AIs without a folder (unlinked, TREE-06) are not part of the tree.
func BuildTree(home string, ais []AIContact) []TreeNode {
	if home != "" {
		home = path.Clean(home)
	}

	entries := make([]AIContact, 0, len(ais))
	folders := make([]string, 0, len(ais))
	byFolder := make(map[string]int, len(ais))
	for _, a := range ais {
		if a.Folder == "" {
			continue
		}
		folder := path.Clean(a.Folder)
		if seen, ok := byFolder[folder]; ok {
			if a.ID < entries[seen].ID {
				entries[seen] = a
			}
			continue
		}
		byFolder[folder] = len(entries)
		entries = append(entries, a)
		folders = append(folders, folder)
	}

	n := len(entries)
	parent := make([]int, n)
	children := make([][]int, n)
	names := make([]string, n)
	paths := make([]string, n)
	segments := make([][]string, n)
	absolute := make([]bool, n)

	for i, folder := range folders {
		parent[i] = nearestIdentityParent(folder, home, byFolder)
		segments[i], absolute[i] = segmentsUnder(folder, home)
	}

	for i := range parent {
		if parent[i] < 0 {
			continue
		}
		children[parent[i]] = append(children[parent[i]], i)
	}

	for i := range entries {
		start := 0
		prefix := ""
		if parent[i] < 0 {
			if absolute[i] && len(segments[i]) > 1 {
				prefix = "/"
			}
		} else {
			start = len(segments[parent[i]])
			if start > len(segments[i])-1 {
				start = len(segments[i]) - 1
			}
		}
		names[i] = nodeName(segments[i], absolute[i])
		if len(segments[i])-1 > start {
			paths[i] = prefix + strings.Join(segments[i][start:len(segments[i])-1], "/")
		}
	}

	var roots []int
	for i := range parent {
		if parent[i] < 0 {
			roots = append(roots, i)
		}
	}

	sortNodes := func(ids []int) {
		sort.SliceStable(ids, func(a, b int) bool {
			i, j := ids[a], ids[b]
			ki, kj := nodeSortKey(paths[i], names[i]), nodeSortKey(paths[j], names[j])
			if ki != kj {
				return ki < kj
			}
			if paths[i] != paths[j] {
				return paths[i] < paths[j]
			}
			if names[i] != names[j] {
				return names[i] < names[j]
			}
			if folders[i] != folders[j] {
				return folders[i] < folders[j]
			}
			return entries[i].ID < entries[j].ID
		})
	}

	sortNodes(roots)
	for i := range children {
		sortNodes(children[i])
	}

	var render func(i int) TreeNode
	render = func(i int) TreeNode {
		var kids []TreeNode
		if len(children[i]) > 0 {
			kids = make([]TreeNode, len(children[i]))
			for k, child := range children[i] {
				kids[k] = render(child)
			}
		}
		return TreeNode{AI: entries[i], Path: paths[i], Name: names[i], Children: kids}
	}

	if len(roots) == 0 {
		return nil
	}
	nodes := make([]TreeNode, len(roots))
	for i, root := range roots {
		nodes[i] = render(root)
	}
	return nodes
}

// BuildMachines groups each machine's AIs into its folder tree and sorts machines with an
// online AI first, then by display label (label, else hostname), case-insensitively
// (TREE-03).
func BuildMachines(machines []MachineInput) []MachineTree {
	trees := make([]MachineTree, len(machines))
	for i, m := range machines {
		ais := make([]AIContact, len(m.AIs))
		copy(ais, m.AIs)
		online := false
		for k := range ais {
			if ais[k].Machine == "" {
				ais[k].Machine = m.Hostname
			}
			if ais[k].Online {
				online = true
			}
		}
		label := m.Label
		if label == "" {
			label = m.Hostname
		}
		trees[i] = MachineTree{
			Hostname: m.Hostname,
			Label:    label,
			Online:   online,
			Nodes:    BuildTree(m.Home, ais),
		}
	}

	sort.SliceStable(trees, func(i, j int) bool {
		if trees[i].Online != trees[j].Online {
			return trees[i].Online
		}
		li, lj := strings.ToLower(trees[i].Label), strings.ToLower(trees[j].Label)
		if li != lj {
			return li < lj
		}
		return strings.ToLower(trees[i].Hostname) < strings.ToLower(trees[j].Hostname)
	})
	return trees
}

func nearestIdentityParent(folder, home string, byFolder map[string]int) int {
	if folder == "/" || folder == home {
		return -1
	}
	underHome := home != "" && (home == "/" || strings.HasPrefix(folder, home+"/"))
	for p := path.Dir(folder); ; p = path.Dir(p) {
		if idx, ok := byFolder[p]; ok {
			return idx
		}
		if underHome && p == home {
			return -1
		}
		if p == "/" || p == "." {
			return -1
		}
	}
}

func segmentsUnder(folder, home string) ([]string, bool) {
	if home != "" {
		if home == "/" {
			if folder == "/" {
				return nil, false
			}
			return splitPath(folder), false
		}
		if folder == home {
			return nil, false
		}
		if strings.HasPrefix(folder, home+"/") {
			return splitPath(strings.TrimPrefix(folder, home+"/")), false
		}
	}
	if folder == "/" {
		return nil, true
	}
	return splitPath(folder), true
}

func splitPath(folder string) []string {
	folder = strings.Trim(folder, "/")
	if folder == "" {
		return nil
	}
	return strings.Split(folder, "/")
}

func nodeName(segments []string, absolute bool) string {
	if len(segments) == 0 {
		if absolute {
			return "/"
		}
		return "~"
	}
	return segments[len(segments)-1]
}

func nodeSortKey(pathValue, name string) string {
	if pathValue == "" {
		return strings.ToLower(name)
	}
	return strings.ToLower(pathValue + " › " + name)
}
