// SPDX-License-Identifier: Apache-2.0

package wm

import "encoding/json"

// nodeDTO is the wire shape of one layout node.
type nodeDTO struct {
	Leaf  bool     `json:"leaf"`
	Role  int      `json:"role,omitempty"`
	Vert  bool     `json:"vert,omitempty"`
	Ratio float64  `json:"ratio,omitempty"`
	A     *nodeDTO `json:"a,omitempty"`
	B     *nodeDTO `json:"b,omitempty"`
}

type treeDTO struct {
	Root    *nodeDTO `json:"root"`
	Focused int      `json:"focused"`
}

// Marshal serializes the layout and focused role so a detached client can restore it on attach.
func (t *Tree) Marshal() []byte {
	b, _ := json.Marshal(treeDTO{Root: toDTO(t.root), Focused: t.FocusedRole()})
	return b
}

func toDTO(n *node) *nodeDTO {
	if n == nil {
		return nil
	}
	return &nodeDTO{Leaf: n.leaf, Role: n.role, Vert: n.vert, Ratio: n.ratio, A: toDTO(n.a), B: toDTO(n.b)}
}

// Unmarshal restores a Marshal-ed layout; a nil error guarantees a usable focused tree.
func Unmarshal(b []byte) (*Tree, error) {
	var dto treeDTO
	if err := json.Unmarshal(b, &dto); err != nil {
		return nil, err
	}
	root := fromDTO(dto.Root, nil)
	if root == nil {
		root = &node{leaf: true}
	}
	t := &Tree{root: root}
	t.focused = firstLeaf(root)
	t.FocusRole(dto.Focused)
	return t, nil
}

func fromDTO(d *nodeDTO, parent *node) *node {
	if d == nil {
		return nil
	}
	n := &node{leaf: d.Leaf, role: d.Role, vert: d.Vert, ratio: clampRatio(d.Ratio), parent: parent}
	if !n.leaf {
		n.a, n.b = fromDTO(d.A, n), fromDTO(d.B, n)
		if n.a == nil || n.b == nil {
			n.leaf, n.a, n.b = true, nil, nil // malformed split degrades to a leaf
		}
	}
	return n
}

func firstLeaf(n *node) *node {
	for !n.leaf {
		n = n.a
	}
	return n
}
