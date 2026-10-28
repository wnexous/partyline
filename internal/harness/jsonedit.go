package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The JSON editor changes one member or array element of a harness config file and keeps
// every other byte as it was (HAR-02): it parses the file into value spans and splices raw
// text instead of re-encoding the document.

// node is a parsed JSON value with its byte span [start, end).
type node struct {
	kind       byte // '{', '[' or 0 for a scalar
	start, end int
	members    []member // objects
	elems      []*node  // arrays
}

type member struct {
	key      string
	keyStart int
	val      *node
}

func (n *node) get(key string) *node {
	if n == nil || n.kind != '{' {
		return nil
	}
	for i := len(n.members) - 1; i >= 0; i-- { // the last duplicate wins, like encoding/json
		if n.members[i].key == key {
			return n.members[i].val
		}
	}
	return nil
}

func (n *node) index(key string) int {
	for i := len(n.members) - 1; i >= 0; i-- {
		if n.members[i].key == key {
			return i
		}
	}
	return -1
}

// ParseError is a harness config file that is not valid JSON; setup leaves such files alone.
type ParseError struct {
	Path string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s is not valid JSON (%v); fix or move it and run partyline setup again", e.Path, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

type parser struct {
	data []byte
	pos  int
}

// parseDoc parses data, which must be valid JSON whose top level is an object. Empty or
// whitespace-only data reads as an empty object.
func parseDoc(data []byte) (*node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	if !json.Valid(data) {
		var v any
		err := json.Unmarshal(data, &v)
		if err == nil {
			err = errors.New("invalid JSON")
		}
		return nil, err
	}
	p := &parser{data: data}
	p.ws()
	root := p.value()
	if root.kind != '{' {
		return nil, errors.New("the top level is not an object")
	}
	return root, nil
}

func (p *parser) ws() {
	for p.pos < len(p.data) {
		switch p.data[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// value parses the value at pos; the input is known to be valid JSON.
func (p *parser) value() *node {
	n := &node{start: p.pos}
	switch p.data[p.pos] {
	case '{':
		n.kind = '{'
		p.pos++
		p.ws()
		for p.data[p.pos] != '}' {
			keyStart := p.pos
			key := p.str()
			p.ws()
			p.pos++ // ':'
			p.ws()
			v := p.value()
			n.members = append(n.members, member{key: key, keyStart: keyStart, val: v})
			p.ws()
			if p.data[p.pos] == ',' {
				p.pos++
				p.ws()
			}
		}
		p.pos++
	case '[':
		n.kind = '['
		p.pos++
		p.ws()
		for p.data[p.pos] != ']' {
			n.elems = append(n.elems, p.value())
			p.ws()
			if p.data[p.pos] == ',' {
				p.pos++
				p.ws()
			}
		}
		p.pos++
	case '"':
		p.str()
	default:
		for p.pos < len(p.data) && !strings.ContainsRune(",}] \t\n\r", rune(p.data[p.pos])) {
			p.pos++
		}
	}
	n.end = p.pos
	return n
}

// str parses a string at pos and returns its decoded value.
func (p *parser) str() string {
	start := p.pos
	p.pos++
	for p.data[p.pos] != '"' {
		if p.data[p.pos] == '\\' {
			p.pos++
		}
		p.pos++
	}
	p.pos++
	var s string
	_ = json.Unmarshal(p.data[start:p.pos], &s)
	return s
}

// lineIndent returns the whitespace that starts the line containing pos.
func lineIndent(data []byte, pos int) string {
	lineStart := bytes.LastIndexByte(data[:pos], '\n') + 1
	end := lineStart
	for end < len(data) && (data[end] == ' ' || data[end] == '\t') {
		end++
	}
	return string(data[lineStart:end])
}

// marshal encodes v as JSON; with pretty set, nested lines start with indent.
func marshal(v any, pretty bool, indent string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent(indent, "  ")
	}
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// pretty reports whether the container n spans several lines.
func pretty(data []byte, n *node) bool {
	return bytes.IndexByte(data[n.start:n.end], '\n') >= 0
}

func splice(data []byte, start, end int, insert []byte) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(insert))
	out = append(out, data[:start]...)
	out = append(out, insert...)
	return append(out, data[end:]...)
}

// insertItem adds raw text (a "key": value member or an array element, rendered by render
// with the indent of its line) as the last item of container c.
func insertItem(data []byte, c *node, count int, lastEnd int, render func(pretty bool, indent string) []byte) []byte {
	closePos := c.end - 1
	if count == 0 {
		multiline := pretty(data, c) || c.start == 0 || bytes.IndexByte(data, '\n') >= 0
		if !multiline {
			return splice(data, c.start+1, closePos, render(false, ""))
		}
		outer := lineIndent(data, c.start)
		inner := outer + "  "
		text := append([]byte("\n"+inner), render(true, inner)...)
		text = append(text, []byte("\n"+outer)...)
		return splice(data, c.start+1, closePos, text)
	}
	if !pretty(data, c) {
		return splice(data, lastEnd, lastEnd, append([]byte(","), render(false, "")...))
	}
	var itemStart int
	if c.kind == '{' {
		itemStart = c.members[count-1].keyStart
	} else {
		itemStart = c.elems[count-1].start
	}
	indent := lineIndent(data, itemStart)
	text := append([]byte(",\n"+indent), render(true, indent)...)
	return splice(data, lastEnd, lastEnd, text)
}

// removeItem deletes item i of container c (span itemStart..itemEnd) with its separator, the
// exact inverse of insertItem for the last item.
func removeItem(data []byte, c *node, count, i, itemStart, itemEnd int, prevEnd func(int) int, nextStart func(int) int) []byte {
	switch {
	case count == 1:
		return splice(data, c.start+1, c.end-1, nil)
	case i == count-1:
		return splice(data, prevEnd(i-1), itemEnd, nil)
	default:
		return splice(data, itemStart, nextStart(i+1), nil)
	}
}

// docEdit holds a config document being edited.
type docEdit struct {
	path string
	data []byte
}

func (d *docEdit) root() (*node, error) {
	n, err := parseDoc(d.data)
	if err != nil {
		return nil, &ParseError{Path: d.path, Err: err}
	}
	return n, nil
}

// lookup returns the value at keys, or nil.
func (d *docEdit) lookup(keys ...string) (*node, error) {
	n, err := d.root()
	if err != nil || n == nil {
		return nil, err
	}
	for _, k := range keys {
		n = n.get(k)
		if n == nil {
			return nil, nil
		}
	}
	return n, nil
}

// raw returns the bytes of n.
func (d *docEdit) raw(n *node) []byte { return d.data[n.start:n.end] }

// set makes keys point to v: an existing value is replaced in place, a missing member is
// added as the last member of its object, and missing objects on the way are created.
func (d *docEdit) set(v any, keys ...string) error {
	root, err := d.root()
	if err != nil {
		return err
	}
	if root == nil {
		d.data = []byte("{}\n")
		root, _ = d.root()
	}
	obj := root
	for i, k := range keys {
		next := obj.get(k)
		if next == nil {
			val := v
			for j := len(keys) - 1; j > i; j-- {
				val = orderedObject{{keys[j], val}}
			}
			lastEnd := 0
			if n := len(obj.members); n > 0 {
				lastEnd = obj.members[n-1].val.end
			}
			d.data = insertItem(d.data, obj, len(obj.members), lastEnd, func(pretty bool, indent string) []byte {
				sep := ":"
				if pretty {
					sep = ": "
				}
				return append(marshal(k, false, ""), append([]byte(sep), marshal(val, pretty, indent)...)...)
			})
			return nil
		}
		if i == len(keys)-1 {
			indent := lineIndent(d.data, next.start)
			if mi := obj.index(k); mi >= 0 {
				indent = lineIndent(d.data, obj.members[mi].keyStart)
			}
			d.data = splice(d.data, next.start, next.end, marshal(v, pretty(d.data, obj), indent))
			return nil
		}
		if next.kind != '{' {
			return fmt.Errorf("%s: %q is not an object", d.path, strings.Join(keys[:i+1], "."))
		}
		obj = next
	}
	return nil
}

// del removes the member at keys and reports whether it existed. Objects on the way that
// become empty are removed too.
func (d *docEdit) del(keys ...string) (bool, error) {
	obj, err := d.objectAt(keys[:len(keys)-1])
	if err != nil || obj == nil {
		return false, err
	}
	i := obj.index(keys[len(keys)-1])
	if i < 0 {
		return false, nil
	}
	d.data = removeMember(d.data, obj, i)
	for depth := len(keys) - 1; depth > 0; depth-- {
		o, _ := d.objectAt(keys[:depth])
		if o == nil || len(o.members) > 0 {
			break
		}
		parent, _ := d.objectAt(keys[:depth-1])
		d.data = removeMember(d.data, parent, parent.index(keys[depth-1]))
	}
	return true, nil
}

// objectAt returns the object at keys, or nil when it is missing or not an object.
func (d *docEdit) objectAt(keys []string) (*node, error) {
	n, err := d.root()
	if err != nil || n == nil {
		return nil, err
	}
	for _, k := range keys {
		n = n.get(k)
		if n == nil || n.kind != '{' {
			return nil, nil
		}
	}
	return n, nil
}

func removeMember(data []byte, obj *node, i int) []byte {
	m := obj.members[i]
	return removeItem(data, obj, len(obj.members), i, m.keyStart, m.val.end,
		func(j int) int { return obj.members[j].val.end },
		func(j int) int { return obj.members[j].keyStart })
}

// appendElem adds v as the last element of the array at keys, creating the array (and the
// objects on the way) when missing.
func (d *docEdit) appendElem(v any, keys ...string) error {
	arr, err := d.lookup(keys...)
	if err != nil {
		return err
	}
	if arr == nil {
		return d.set([]any{v}, keys...)
	}
	if arr.kind != '[' {
		return fmt.Errorf("%s: %q is not an array", d.path, strings.Join(keys, "."))
	}
	lastEnd := 0
	if n := len(arr.elems); n > 0 {
		lastEnd = arr.elems[n-1].end
	}
	d.data = insertItem(d.data, arr, len(arr.elems), lastEnd, func(pretty bool, indent string) []byte {
		return marshal(v, pretty, indent)
	})
	return nil
}

// removeElems deletes the elements of the array at keys for which match is true (it is
// given each element's raw bytes) and returns how many were removed. An array left empty
// is removed with the objects on the way that become empty.
func (d *docEdit) removeElems(match func(raw []byte) bool, keys ...string) (int, error) {
	removed := 0
	for {
		arr, err := d.lookup(keys...)
		if err != nil || arr == nil || arr.kind != '[' {
			return removed, err
		}
		idx := -1
		for i, e := range arr.elems {
			if match(d.raw(e)) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return removed, nil
		}
		removed++
		if len(arr.elems) == 1 {
			_, err := d.del(keys...)
			return removed, err
		}
		e := arr.elems[idx]
		d.data = removeItem(d.data, arr, len(arr.elems), idx, e.start, e.end,
			func(j int) int { return arr.elems[j].end },
			func(j int) int { return arr.elems[j].start })
	}
}

// empty reports whether the document is an empty object (or empty text).
func (d *docEdit) empty() bool {
	root, err := d.root()
	return err == nil && (root == nil || len(root.members) == 0)
}

// orderedObject is a JSON object that keeps its member order.
type orderedObject []kv

type kv struct {
	Key   string
	Value any
}

// MarshalJSON writes the members in order.
func (o orderedObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(marshal(m.Key, false, ""))
		buf.WriteByte(':')
		buf.Write(marshal(m.Value, false, ""))
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
