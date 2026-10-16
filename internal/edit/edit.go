// Package edit applies structured changes to the spec files under system/.
//
// List items (components, layers, technologies, ...) are spliced into the
// original text, so comments, blank lines, and every untouched item stay
// byte-for-byte the same and proposal diffs show only what changed. Files
// that hold a single document (agent charters, decision front matter) are
// re-encoded, merging into the existing YAML so comments and styles survive.
package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

// Change edits one spec item. Value is the item's new content as a JSON
// object, with keys in the order they should be written; null deletes the
// item. ID names the existing item ("" for a new decision, whose id is
// assigned). Body is a decision's Markdown body.
//
// A Change with Order instead reorders the list of its kind: the items it
// names take, in that order, the places those items held. Items it doesn't
// name keep their places, and names of items not in the list are ignored,
// so the order stays valid when other changes add, delete, or rename items.
type Change struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id,omitempty"`
	Value json.RawMessage `json:"value"`
	Body  string          `json:"body,omitempty"`
	Order []string        `json:"order,omitempty"`
}

// collection describes where a list-kind item lives.
type collection struct {
	file, key, idField string
}

var collections = map[string]collection{
	"component":   {spec.Dir + "/system.yaml", "components", "id"},
	"layer":       {spec.Dir + "/system.yaml", "layers", "id"},
	"technology":  {spec.Dir + "/tech.yaml", "technologies", "id"},
	"principle":   {spec.Dir + "/principles.yaml", "principles", "id"},
	"domain":      {spec.Dir + "/domains.yaml", "domains", "id"},
	"term":        {spec.Dir + "/domains.yaml", "glossary", "term"},
	"milestone":   {spec.Dir + "/roadmap.yaml", "milestones", "id"},
	"requirement": {spec.Dir + "/requirements.yaml", "requirements", "id"},
	"risk":        {spec.Dir + "/risks.yaml", "risks", "id"},
	"event":       {spec.Dir + "/events.yaml", "events", "id"},
}

// Kinds lists every kind a Change may have.
func Kinds() []string {
	kinds := []string{"agent", "decision"}
	for k := range collections {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// Files is the result of applying changes: new file contents by path
// relative to the repository root, with nil meaning the file is deleted.
type Files map[string][]byte

// Apply applies changes, in order, to the repository at root without
// writing anything. today (YYYY-MM-DD) dates new decisions.
func Apply(root string, changes []Change, today string) (Files, error) {
	out := Files{}
	read := func(path string) ([]byte, error) {
		if data, ok := out[path]; ok {
			if data == nil {
				return nil, os.ErrNotExist
			}
			return data, nil
		}
		return os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	}
	for i, c := range changes {
		if err := apply(root, read, out, c, today); err != nil {
			return nil, fmt.Errorf("change %d (%s %q): %w", i+1, c.Kind, c.ID, err)
		}
	}
	return out, nil
}

func apply(root string, read func(string) ([]byte, error), out Files, c Change, today string) error {
	if len(c.Order) > 0 {
		col, ok := collections[c.Kind]
		if !ok {
			return fmt.Errorf("%s items can't be reordered", c.Kind)
		}
		data, err := read(col.file)
		if err != nil {
			return err
		}
		updated, err := reorderItems(data, col.key, col.idField, c.Order)
		if err != nil {
			return fmt.Errorf("%s: %w", col.file, err)
		}
		out[col.file] = updated
		return nil
	}
	value, err := valueNode(c.Value)
	if err != nil {
		return err
	}
	if col, ok := collections[c.Kind]; ok {
		data, err := read(col.file)
		if err != nil {
			return err
		}
		updated, err := spliceItem(data, col.key, col.idField, c.ID, value)
		if err != nil {
			return fmt.Errorf("%s: %w", col.file, err)
		}
		out[col.file] = updated
		return nil
	}
	switch c.Kind {
	case "agent":
		return applyAgent(read, out, c, value)
	case "decision":
		return applyDecision(root, read, out, c, value, today)
	}
	return fmt.Errorf("unknown kind %q", c.Kind)
}

func applyAgent(read func(string) ([]byte, error), out Files, c Change, value *yaml.Node) error {
	oldPath := ""
	if c.ID != "" {
		oldPath = spec.Dir + "/agents/" + c.ID + ".yaml"
	}
	if value == nil {
		if oldPath == "" {
			return errors.New("delete needs an id")
		}
		if _, err := read(oldPath); err != nil {
			return err
		}
		out[oldPath] = nil
		return nil
	}
	newID := stringField(value, "id")
	if !idPattern.MatchString(newID) {
		return fmt.Errorf("invalid agent id %q", newID)
	}
	newPath := spec.Dir + "/agents/" + newID + ".yaml"
	var doc *yaml.Node
	if oldPath != "" {
		data, err := read(oldPath)
		if err != nil {
			return err
		}
		if doc, err = parseDoc(data); err != nil {
			return fmt.Errorf("%s: %w", oldPath, err)
		}
		merge(doc, value)
		if newPath != oldPath {
			out[oldPath] = nil
		}
	} else {
		if _, err := read(newPath); err == nil {
			return fmt.Errorf("agent %q already exists", newID)
		}
		doc = value
	}
	data, err := encode(doc)
	if err != nil {
		return err
	}
	out[newPath] = data
	return nil
}

var (
	idPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	decisionPattern = regexp.MustCompile(`^([0-9]{4})-.*\.md$`)
	nonSlug         = regexp.MustCompile(`[^a-z0-9]+`)
	datePattern     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// Slug turns a title into a lowercase, hyphenated file-name fragment.
func Slug(title string, max int) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > max {
		s = strings.TrimRight(s[:max], "-")
	}
	if s == "" {
		s = "change"
	}
	return s
}

func applyDecision(root string, read func(string) ([]byte, error), out Files, c Change, value *yaml.Node, today string) error {
	dir := spec.Dir + "/decisions"
	// Find existing decision files, including ones created by earlier
	// changes in this batch.
	files := map[string]string{} // id -> path
	entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	for _, e := range entries {
		if m := decisionPattern.FindStringSubmatch(e.Name()); m != nil {
			files[m[1]] = dir + "/" + e.Name()
		}
	}
	for p, data := range out {
		if m := decisionPattern.FindStringSubmatch(filepath.Base(p)); m != nil && strings.HasPrefix(p, dir+"/") {
			if data == nil {
				delete(files, m[1])
			} else {
				files[m[1]] = p
			}
		}
	}

	if c.ID == "" {
		if value == nil {
			return errors.New("delete needs an id")
		}
		next := 1
		for id := range files {
			var n int
			fmt.Sscanf(id, "%d", &n)
			if n >= next {
				next = n + 1
			}
		}
		id := fmt.Sprintf("%04d", next)
		setField(value, "id", id, true)
		if stringField(value, "date") == "" {
			setField(value, "date", today, false)
		}
		path := fmt.Sprintf("%s/%s-%s.md", dir, id, Slug(stringField(value, "title"), 50))
		data, err := decisionFile(value, c.Body)
		if err != nil {
			return err
		}
		out[path] = data
		return nil
	}

	path, ok := files[c.ID]
	if !ok {
		return fmt.Errorf("no decision %q", c.ID)
	}
	if value == nil {
		out[path] = nil
		return nil
	}
	data, err := read(path)
	if err != nil {
		return err
	}
	front, _, err := spec.SplitFrontMatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	doc, err := parseDoc(front)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// A decision keeps its id; renumbering would break references to it.
	setField(value, "id", c.ID, true)
	merge(doc, value)
	if out[path], err = decisionFile(doc, c.Body); err != nil {
		return err
	}
	return nil
}

func decisionFile(front *yaml.Node, body string) ([]byte, error) {
	fm, err := encode(front)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")))
	b.WriteString("\n")
	return b.Bytes(), nil
}

// spliceItem replaces, appends, or deletes the item whose idField equals id
// in the top-level sequence under key.
func spliceItem(data []byte, key, idField, id string, value *yaml.Node) ([]byte, error) {
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if !strings.HasSuffix(string(data), "\n") && len(lines) > 0 {
		lines[len(lines)-1] += "\n"
	}

	keyIdx := -1
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			keyIdx = i
		}
	}
	if keyIdx < 0 {
		if value == nil {
			return nil, fmt.Errorf("no %s list", key)
		}
		// Add the list at the end of the file.
		block, err := encodeItems(value, 2)
		if err != nil {
			return nil, err
		}
		out := strings.Join(lines, "") + "\n" + key + ":\n" + block
		return []byte(out), nil
	}
	seq := doc.Content[keyIdx+1]
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s is not a block list", key)
	}
	// The first line after the list: the next top-level key, or EOF.
	listEnd := len(lines)
	if keyIdx+2 < len(doc.Content) {
		listEnd = startLine(doc.Content[keyIdx+2], lines) - 1
	}

	match := -1
	if id != "" {
		for i, item := range seq.Content {
			if stringField(item, idField) == id {
				match = i
			}
		}
		if match < 0 {
			return nil, fmt.Errorf("no %s with %s %q", strings.TrimSuffix(key, "s"), idField, id)
		}
	}
	if value != nil {
		newID := stringField(value, idField)
		for i, item := range seq.Content {
			if i != match && stringField(item, idField) == newID {
				return nil, fmt.Errorf("%s %q already exists", idField, newID)
			}
		}
	}

	// Item i spans [start(i), start(i+1)) less trailing blank and comment
	// lines, which belong to the gap before the next item.
	itemRange := func(i int) (int, int) {
		start := seq.Content[i].Line - 1
		end := listEnd
		if i+1 < len(seq.Content) {
			end = startLine(seq.Content[i+1], lines) - 1
		}
		for end > start+1 && isGap(lines[end-1]) {
			end--
		}
		return start, end
	}
	indent := 0
	separated := false // items separated by blank lines
	if len(seq.Content) > 0 {
		indent = seq.Content[0].Column - 3
		if len(seq.Content) > 1 {
			_, end := itemRange(0)
			separated = strings.TrimSpace(lines[end]) == ""
		}
	} else {
		indent = 2
	}
	if indent < 0 {
		indent = 0
	}

	switch {
	case value == nil: // delete
		start, end := itemRange(match)
		// Drop the gap before the next item, or before this one if last.
		if match+1 < len(seq.Content) {
			end = startLine(seq.Content[match+1], lines) - 1
		} else if match > 0 {
			_, prevEnd := itemRange(match - 1)
			start = prevEnd
		}
		return []byte(strings.Join(lines[:start], "") + strings.Join(lines[end:], "")), nil
	case match >= 0: // replace
		item := seq.Content[match]
		merge(item, value)
		// The head comment sits in the gap before the item and stays there.
		item.HeadComment = ""
		block, err := encodeItems(item, indent)
		if err != nil {
			return nil, err
		}
		start, end := itemRange(match)
		return []byte(strings.Join(lines[:start], "") + block + strings.Join(lines[end:], "")), nil
	default: // append
		block, err := encodeItems(value, indent)
		if err != nil {
			return nil, err
		}
		at := listEnd
		if len(seq.Content) > 0 {
			_, at = itemRange(len(seq.Content) - 1)
		}
		if separated {
			block = "\n" + block
		}
		return []byte(strings.Join(lines[:at], "") + block + strings.Join(lines[at:], "")), nil
	}
}

// reorderItems moves the named items of the top-level sequence under key
// into the given order, each with its head comment. The gaps between items
// stay where they are.
func reorderItems(data []byte, key, idField string, order []string) ([]byte, error) {
	doc, err := parseDoc(data)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if !strings.HasSuffix(string(data), "\n") && len(lines) > 0 {
		lines[len(lines)-1] += "\n"
	}
	keyIdx := -1
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			keyIdx = i
		}
	}
	if keyIdx < 0 {
		return nil, fmt.Errorf("no %s list", key)
	}
	seq := doc.Content[keyIdx+1]
	if seq.Kind != yaml.SequenceNode || seq.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s is not a block list", key)
	}
	listEnd := len(lines)
	if keyIdx+2 < len(doc.Content) {
		listEnd = startLine(doc.Content[keyIdx+2], lines) - 1
	}
	n := len(seq.Content)
	if n == 0 {
		return data, nil
	}
	// Item i is lines [starts[i], ends[i]); the gap after it runs to the
	// next item's start.
	starts, ends := make([]int, n), make([]int, n)
	for i, item := range seq.Content {
		starts[i] = startLine(item, lines) - 1
	}
	for i := range seq.Content {
		end := listEnd
		if i+1 < n {
			end = starts[i+1]
		}
		for end > seq.Content[i].Line && isGap(lines[end-1]) {
			end--
		}
		ends[i] = end
	}

	index := map[string]int{}
	for i, item := range seq.Content {
		index[stringField(item, idField)] = i
	}
	var named []int // the items the order names, in that order
	seen := map[int]bool{}
	for _, id := range order {
		if i, ok := index[id]; ok && !seen[i] {
			named = append(named, i)
			seen[i] = true
		}
	}
	// The named items' places, in list order, are filled in the new order.
	place := make([]int, n)
	next := 0
	for i := range place {
		place[i] = i
		if seen[i] {
			place[i] = named[next]
			next++
		}
	}

	var b strings.Builder
	b.WriteString(strings.Join(lines[:starts[0]], ""))
	for i := range seq.Content {
		j := place[i]
		b.WriteString(strings.Join(lines[starts[j]:ends[j]], ""))
		gapEnd := listEnd
		if i+1 < n {
			gapEnd = starts[i+1]
		}
		b.WriteString(strings.Join(lines[ends[i]:gapEnd], ""))
	}
	b.WriteString(strings.Join(lines[listEnd:], ""))
	return []byte(b.String()), nil
}

// startLine returns the 1-based line where n begins, including head
// comments attached to it.
func startLine(n *yaml.Node, lines []string) int {
	line := n.Line
	if n.HeadComment != "" {
		line -= strings.Count(n.HeadComment, "\n") + 1
		// Head comments may be separated from the node by blank lines.
		for line > 1 && strings.TrimSpace(lines[line-1]) == "" {
			line--
		}
	}
	return line
}

func isGap(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// encodeItems encodes n as one block-sequence item indented by indent spaces.
func encodeItems(n *yaml.Node, indent int) (string, error) {
	data, err := encode(&yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{n}})
	if err != nil {
		return "", err
	}
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if l == "" {
			continue
		}
		if strings.TrimSpace(l) != "" {
			b.WriteString(pad)
		}
		b.WriteString(l)
	}
	return b.String(), nil
}

func encode(n *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func parseDoc(data []byte) (*yaml.Node, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(data, &n); err != nil {
		return nil, err
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("expected a YAML mapping")
	}
	return n.Content[0], nil
}

// valueNode parses a JSON value into a YAML node, keeping key order, or
// returns nil for null.
func valueNode(raw json.RawMessage) (*yaml.Node, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	if !json.Valid(raw) {
		return nil, errors.New("value is not valid JSON")
	}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	v := n.Content[0]
	if v.Kind != yaml.MappingNode {
		return nil, errors.New("value must be an object")
	}
	prune(v)
	restyle(v)
	return v, nil
}

// prune drops nulls, empty strings, and empty objects, which the spec
// omits rather than writes. Empty lists are kept: some are required
// (may_depend_on: []), and the client omits optional ones.
func prune(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Tag == "!!null" || (n.Tag == "!!str" && n.Value == "")
	case yaml.SequenceNode:
		kept := n.Content[:0]
		for _, c := range n.Content {
			if !prune(c) {
				kept = append(kept, c)
			}
		}
		n.Content = kept
		return false
	case yaml.MappingNode:
		kept := n.Content[:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			if !prune(n.Content[i+1]) {
				kept = append(kept, n.Content[i], n.Content[i+1])
			}
		}
		n.Content = kept
		return len(kept) == 0
	}
	return false
}

// restyle turns JSON's flow style into the spec's block style, keeping
// short lists of scalars inline: tech: [go, grpc].
func restyle(n *yaml.Node) {
	n.Style = 0
	if n.Kind == yaml.ScalarNode {
		// Keep dates plain (2026-10-06) rather than quoted, as written by hand.
		if n.Tag == "!!str" && datePattern.MatchString(n.Value) {
			n.Tag = "!!timestamp"
		}
		return
	}
	for _, c := range n.Content {
		restyle(c)
	}
	if n.Kind == yaml.SequenceNode && inlineList(n) {
		n.Style = yaml.FlowStyle
	}
}

func inlineList(n *yaml.Node) bool {
	total := 0
	for _, c := range n.Content {
		if c.Kind != yaml.ScalarNode || strings.ContainsAny(c.Value, ",:{}[]#\n") || len(c.Value) > 40 {
			return false
		}
		total += len(c.Value) + 2
	}
	return total <= 72
}

// merge updates old in place to match new, keeping old's comments, key
// order, and styles where the shapes agree.
func merge(old, new *yaml.Node) {
	if old.Kind != new.Kind {
		head, line := old.HeadComment, old.LineComment
		*old = *new
		old.HeadComment, old.LineComment = head, line
		return
	}
	switch old.Kind {
	case yaml.ScalarNode:
		if old.Value == new.Value {
			return
		}
		old.Value, old.Tag = new.Value, new.Tag
		if old.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 && !strings.Contains(new.Value, "\n") {
			old.Style = 0
		}
	case yaml.SequenceNode:
		n := min(len(old.Content), len(new.Content))
		for i := 0; i < n; i++ {
			merge(old.Content[i], new.Content[i])
		}
		old.Content = append(old.Content[:n], new.Content[n:]...)
		if old.Style&yaml.FlowStyle != 0 && !inlineList(old) {
			old.Style = 0
		}
	case yaml.MappingNode:
		var kept []*yaml.Node
		for i := 0; i+1 < len(old.Content); i += 2 {
			if v := lookup(new, old.Content[i].Value); v != nil {
				merge(old.Content[i+1], v)
				kept = append(kept, old.Content[i], old.Content[i+1])
			}
		}
		for i := 0; i+1 < len(new.Content); i += 2 {
			if lookup(old, new.Content[i].Value) == nil {
				kept = append(kept, new.Content[i], new.Content[i+1])
			}
		}
		old.Content = kept
	}
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	if m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func stringField(m *yaml.Node, key string) string {
	if v := lookup(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

// setField sets a string field, adding it first or last if missing.
func setField(m *yaml.Node, key, value string, first bool) {
	tag := "!!str"
	if datePattern.MatchString(value) {
		tag = "!!timestamp" // written plain, like hand-written dates
	}
	if v := lookup(m, key); v != nil {
		if v.Value != value {
			v.Kind, v.Tag, v.Value, v.Style = yaml.ScalarNode, tag, value, 0
		}
		return
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	if first {
		m.Content = append([]*yaml.Node{k, v}, m.Content...)
	} else {
		m.Content = append(m.Content, k, v)
	}
}
