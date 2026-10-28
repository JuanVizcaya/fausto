package code

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/check"
	"github.com/JuanVizcaya/fausto/internal/spec"
)

// maxSourceLines caps the source kept for one declaration.
const maxSourceLines = 120

// index is the parsed Go code of every component in the repository.
type index struct {
	pkgs  map[string]*pkg // by directory
	comps map[string]*spec.Component
	calls []Call
}

type pkg struct {
	Package
	component string
	types     map[string]*typ
	funcs     map[string]*fn
	// contract reports whether the package is a generated contract.
	contract bool
	imports  map[string]bool // repository package directories

}

type typ struct {
	Type
	pkg  *pkg
	file *file
	expr ast.Expr
	// fields maps field names to the type each refers to, for calls
	// through a field.
	fields   map[string]ref
	methods  map[string]*fn
	embedded []ref
	// iface lists an interface's own methods; embedded interfaces are in
	// embedded.
	iface   []string
	message bool // a generated protobuf message
}

type fn struct {
	id      string
	decl    *ast.FuncDecl
	file    *file
	pkg     *pkg
	recv    string // receiver type name, for methods
	results []ref
	// arity is the method's shape, params/results, which tells apart
	// methods of the same name (a gRPC server's and client's).
	arity  string
	method *Method
	fun    *Func
}

type file struct {
	path    string
	fset    *token.FileSet
	src     []byte
	imports map[string]string // name in the file → repository package directory
}

// ref names a type: its package directory and name.
type ref struct{ dir, name string }

func (r ref) id() string { return r.dir + "#" + r.name }

func build(repo *check.Repo) (*index, error) {
	resolve, _ := repo.GoImportResolver()
	ix := &index{pkgs: map[string]*pkg{}, comps: map[string]*spec.Component{}}
	for i := range repo.Spec.System.Components {
		c := &repo.Spec.System.Components[i]
		ix.comps[c.ID] = c
	}
	fset := token.NewFileSet()
	var files []*ast.File
	var infos []*file
	for _, f := range repo.Files {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		c := repo.ComponentOf(f)
		if c == nil {
			continue
		}
		src, err := repo.Read(f)
		if err != nil {
			continue
		}
		af, err := parser.ParseFile(fset, f, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			continue // a file that doesn't parse is left out
		}
		dir := path.Dir(f)
		p := ix.pkgs[dir]
		if p == nil {
			p = &pkg{
				Package:   Package{Dir: dir, Name: af.Name.Name, Role: "internal", Imports: []Import{}, External: []string{}, Types: []Type{}, Funcs: []Func{}, Relations: []Relation{}},
				component: c.ID,
				types:     map[string]*typ{},
				funcs:     map[string]*fn{},
				imports:   map[string]bool{},
			}
			for _, ct := range c.Contracts {
				for _, d := range []string{ct.Generated, ct.Go} {
					if d != "" && (dir == d || strings.HasPrefix(dir, d+"/")) {
						p.contract = true
					}
				}
			}
			ix.pkgs[dir] = p
		}
		if af.Name.Name == "main" {
			p.Role = "cmd"
		}
		if ast.IsGenerated(af) {
			p.Role = "generated"
		}
		if af.Doc != nil && p.Doc == "" {
			p.Doc = strings.TrimSpace(af.Doc.Text())
		}
		p.Files = append(p.Files, f)
		info := &file{path: f, fset: fset, src: src, imports: map[string]string{}}
		for _, is := range af.Imports {
			imp, _ := strconv.Unquote(is.Path.Value)
			if dir, ok := resolve(imp); ok {
				name := path.Base(imp)
				if is.Name != nil {
					name = is.Name.Name
				}
				info.imports[name] = dir
				p.imports[dir] = true
			} else {
				p.External = append(p.External, imp)
				if usesDatabase(imp) {
					p.Database = true
				}
			}
		}
		files = append(files, af)
		infos = append(infos, info)
	}
	// Declarations first, so methods and references resolve across files.
	for i, af := range files {
		ix.declare(ix.pkgs[path.Dir(infos[i].path)], infos[i], af)
	}
	ix.fixImportNames(files, infos)
	for i, af := range files {
		ix.describe(ix.pkgs[path.Dir(infos[i].path)], infos[i], af)
	}
	for _, p := range ix.pkgs {
		ix.relate(p)
		p.External = uniqueSorted(p.External)
		sort.Strings(p.Files)
	}
	for i, af := range files {
		ix.collectCalls(ix.pkgs[path.Dir(infos[i].path)], infos[i], af)
	}
	ix.linkRPCs()
	return ix, nil
}

// linkRPCs links each method of a generated gRPC client interface
// (FooClient) to the methods that implement the matching server interface
// (FooServer) outside generated code: the call crosses a process boundary,
// but it's how a gateway handler reaches a service.
func (ix *index) linkRPCs() {
	impls := map[string][]string{}
	for _, dir := range sortedKeys(ix.pkgs) {
		for _, r := range ix.pkgs[dir].Relations {
			if r.Kind == "implements" {
				impls[r.To] = append(impls[r.To], r.From)
			}
		}
	}
	for _, dir := range sortedKeys(ix.pkgs) {
		p := ix.pkgs[dir]
		if p.Role != "generated" {
			continue
		}
		for _, name := range sortedKeys(p.types) {
			svc, ok := strings.CutSuffix(name, "Client")
			client := p.types[name]
			server := p.types[svc+"Server"]
			if !ok || client.Kind != "interface" || server == nil || server.Kind != "interface" {
				continue
			}
			for _, m := range client.iface {
				if server.methods[m] == nil {
					continue
				}
				for _, impl := range impls[server.ID] {
					d, n, _ := strings.Cut(impl, "#")
					target := ix.method(ref{d, n}, m, map[string]bool{})
					if target == nil || target.pkg.Role == "generated" {
						continue // the Unimplemented stub
					}
					ix.calls = append(ix.calls, Call{From: client.ID + "." + m, To: target.id, Kind: "rpc"})
				}
			}
		}
	}
}

// fixImportNames maps an unnamed import to the imported package's real
// name, which may differ from its directory's base name.
func (ix *index) fixImportNames(files []*ast.File, infos []*file) {
	for i, af := range files {
		info := infos[i]
		for _, is := range af.Imports {
			if is.Name != nil {
				continue
			}
			imp, _ := strconv.Unquote(is.Path.Value)
			base := path.Base(imp)
			dir, ok := info.imports[base]
			if !ok {
				continue
			}
			if p := ix.pkgs[dir]; p != nil && p.Name != base {
				delete(info.imports, base)
				info.imports[p.Name] = dir
			}
		}
	}
}

func usesDatabase(imp string) bool {
	return imp == "database/sql" || strings.Contains(imp, "/pgx") || strings.HasPrefix(imp, "github.com/lib/pq")
}

// declare records the package's types and functions.
func (ix *index) declare(p *pkg, f *file, af *ast.File) {
	for _, d := range af.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, s := range d.Specs {
				ts := s.(*ast.TypeSpec)
				doc := ts.Doc
				if doc == nil && len(d.Specs) == 1 {
					doc = d.Doc
				}
				t := &typ{
					Type: Type{
						ID: ref{p.Dir, ts.Name.Name}.id(), Name: ts.Name.Name, Exported: ts.Name.IsExported(),
						Doc: text(doc), Pos: f.pos(ts.Pos()), Fields: []Field{}, Methods: []Method{},
					},
					pkg: p, file: f, expr: ts.Type, fields: map[string]ref{}, methods: map[string]*fn{},
				}
				var start token.Pos = ts.Pos()
				if len(d.Specs) == 1 {
					start = d.Pos()
				}
				t.Source = f.source(start, ts.End())
				switch ts.Type.(type) {
				case *ast.StructType:
					t.Kind = "struct"
				case *ast.InterfaceType:
					t.Kind = "interface"
				default:
					t.Kind = "named"
					if ts.Assign.IsValid() {
						t.Kind = "alias"
					}
					t.Underlying = f.print(ts.Type)
				}
				p.types[t.Name] = t
			}
		case *ast.FuncDecl:
			// Methods are recorded by describe, once every type is known.
			if d.Recv != nil || d.Name.Name == "init" {
				continue // there may be several inits, and nothing calls them
			}
			x := &fn{decl: d, file: f, pkg: p, id: p.Dir + "#" + d.Name.Name}
			x.fun = &Func{
				ID: x.id, Name: d.Name.Name, Sig: f.sig(d.Type), Exported: d.Name.IsExported(),
				Doc: text(d.Doc), Pos: f.pos(d.Pos()), Source: f.source(d.Pos(), d.End()),
			}
			p.funcs[d.Name.Name] = x
		}
	}
}

// describe fills in fields, methods, and the types that functions return,
// once every package's declarations are known.
func (ix *index) describe(p *pkg, f *file, af *ast.File) {
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 {
			continue
		}
		recv, _ := recvType(fd.Recv.List[0].Type)
		t := p.types[recv]
		if t == nil {
			continue
		}
		x := &fn{
			id: p.Dir + "#" + recv + "." + fd.Name.Name, decl: fd, file: f, pkg: p, recv: recv,
		}
		x.method = &Method{
			ID: x.id, Name: fd.Name.Name, Sig: f.sig(fd.Type), Exported: fd.Name.IsExported(),
			Doc: text(fd.Doc), Pos: f.pos(fd.Pos()), Source: f.source(fd.Pos(), fd.End()),
		}
		_, x.method.Pointer = recvType(fd.Recv.List[0].Type)
		x.results = ix.resultRefs(p, f, fd.Type)
		x.arity = arity(fd.Type)
		t.methods[fd.Name.Name] = x
	}
	for _, x := range p.funcs {
		if x.file == f {
			x.results = ix.resultRefs(p, f, x.decl.Type)
		}
	}
	for _, t := range p.types {
		if t.file != f {
			continue
		}
		switch e := t.expr.(type) {
		case *ast.StructType:
			for _, fl := range e.Fields.List {
				typeStr := f.print(fl.Type)
				r, ok := ix.mainRef(p, f, fl.Type)
				if len(fl.Names) == 0 {
					name := typeStr
					if ok {
						name = r.name
						t.embedded = append(t.embedded, r)
					}
					t.Fields = append(t.Fields, Field{Name: name, Type: typeStr, Embedded: true, Exported: ast.IsExported(strings.TrimPrefix(name, "*"))})
					if ok {
						t.fields[r.name] = r
					}
					continue
				}
				if typeStr == "protoimpl.MessageState" {
					t.message = true
				}
				for _, n := range fl.Names {
					t.Fields = append(t.Fields, Field{Name: n.Name, Type: typeStr, Exported: n.IsExported()})
					if ok {
						t.fields[n.Name] = r
					}
				}
			}
		case *ast.InterfaceType:
			for _, m := range e.Methods.List {
				if len(m.Names) == 0 {
					if r, ok := ix.mainRef(p, f, m.Type); ok {
						t.embedded = append(t.embedded, r)
						t.Fields = append(t.Fields, Field{Name: r.name, Type: f.print(m.Type), Embedded: true, Exported: ast.IsExported(r.name)})
					}
					continue
				}
				ft, ok := m.Type.(*ast.FuncType)
				if !ok {
					continue
				}
				for _, n := range m.Names {
					x := &fn{id: t.ID + "." + n.Name, file: f, pkg: p, recv: t.Name, results: ix.resultRefs(p, f, ft), arity: arity(ft)}
					x.method = &Method{
						ID: x.id, Name: n.Name, Sig: f.sig(ft), Exported: n.IsExported(), Doc: text(m.Doc), Pos: f.pos(n.Pos()),
					}
					t.iface = append(t.iface, n.Name)
					t.methods[n.Name] = x
				}
			}
		}
	}
}

// relate records the relations of the package's types and finishes the
// package's exported view.
func (ix *index) relate(p *pkg) {
	gen := p.Role == "generated"
	seen := map[string]bool{}
	add := func(r Relation) {
		key := r.From + "|" + r.To + "|" + r.Kind
		if seen[key] || r.From == r.To {
			return
		}
		seen[key] = true
		p.Relations = append(p.Relations, r)
	}
	// Interfaces a type here may implement: the package's own and those of
	// the repository packages it imports.
	var candidates []*typ
	for _, name := range sortedKeys(p.types) {
		// An interface of only unexported methods in generated code (or,
		// below, in another package) is a marker met by embedding.
		if t := p.types[name]; t.Kind == "interface" && (!gen || ix.anyExported(t)) {
			candidates = append(candidates, t)
		}
	}
	for _, imp := range ix.importedDirs(p) {
		q := ix.pkgs[imp]
		for _, name := range sortedKeys(q.types) {
			t := q.types[name]
			if t.Kind != "interface" || !t.Exported {
				continue
			}
			if !ix.anyExported(t) {
				continue
			}
			candidates = append(candidates, t)
		}
	}
	for _, name := range sortedKeys(p.types) {
		t := p.types[name]
		if gen && !t.Exported {
			continue
		}
		for _, e := range t.embedded {
			if ix.lookup(e) != nil {
				add(Relation{From: t.ID, To: e.id(), Kind: "embeds"})
			}
		}
		if t.Kind != "interface" {
			ms := ix.methodSet(ref{p.Dir, t.Name}, map[string]bool{})
			for _, i := range candidates {
				if i == t {
					continue
				}
				need := ix.methodSet(ref{i.pkg.Dir, i.Name}, map[string]bool{})
				if len(need) == 0 {
					continue
				}
				all := true
				for m, a := range need {
					if have, ok := ms[m]; !ok || have != a {
						all = false
						break
					}
				}
				if all {
					add(Relation{From: t.ID, To: i.ID, Kind: "implements"})
				}
			}
		}
		if s, ok := t.expr.(*ast.StructType); ok {
			for _, fl := range s.Fields.List {
				if len(fl.Names) == 0 {
					continue
				}
				for _, r := range ix.refs(p, t.file, fl.Type) {
					if ix.lookup(r) == nil {
						continue
					}
					for _, n := range fl.Names {
						if gen && !n.IsExported() {
							continue
						}
						add(Relation{From: t.ID, To: r.id(), Kind: "has", Label: n.Name})
					}
				}
			}
		}
		if gen {
			continue
		}
		// Dependencies through method signatures, within the package.
		for _, m := range sortedKeys(t.methods) {
			x := t.methods[m]
			var ft *ast.FuncType
			if x.decl != nil {
				ft = x.decl.Type
			} else if t.Kind == "interface" {
				ft = ifaceFunc(t.expr, m)
			}
			if ft == nil {
				continue
			}
			for _, r := range ix.refs(p, x.file, ft) {
				if r.dir == p.Dir && r.name != t.Name && p.types[r.name] != nil {
					add(Relation{From: t.ID, To: r.id(), Kind: "uses"})
				}
			}
		}
	}
	// "uses" is implied by a stronger relation between the same types.
	strong := map[string]bool{}
	for _, r := range p.Relations {
		if r.Kind != "uses" {
			strong[r.From+"|"+r.To] = true
		}
	}
	rels := p.Relations[:0]
	for _, r := range p.Relations {
		if r.Kind == "uses" && strong[r.From+"|"+r.To] {
			continue
		}
		rels = append(rels, r)
	}
	p.Relations = rels

	// The exported view. Generated code keeps its API: exported types and
	// functions, and no protobuf message internals.
	for _, name := range sortedKeys(p.types) {
		t := p.types[name]
		if gen && !t.Exported {
			continue
		}
		out := t.Type
		out.Fields = []Field{}
		for _, fl := range t.Fields {
			if gen && !fl.Exported {
				continue
			}
			out.Fields = append(out.Fields, fl)
		}
		out.Methods = []Method{}
		for _, m := range sortedMethods(t) {
			if gen && (t.message || !m.method.Exported) {
				continue
			}
			out.Methods = append(out.Methods, *m.method)
		}
		if gen {
			out.Source = ""
			for i := range out.Methods {
				out.Methods[i].Source = ""
			}
		}
		p.Types = append(p.Types, out)
	}
	for _, name := range sortedKeys(p.funcs) {
		x := p.funcs[name]
		if gen && !x.fun.Exported {
			continue
		}
		out := *x.fun
		if gen {
			out.Source = ""
		}
		p.Funcs = append(p.Funcs, out)
	}
}

func sortedMethods(t *typ) []*fn {
	out := make([]*fn, 0, len(t.methods))
	for _, m := range t.methods {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].method, out[j].method
		// Exported first, then in source order.
		if a.Exported != b.Exported {
			return a.Exported
		}
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		return a.Pos.Line < b.Pos.Line
	})
	return out
}

func ifaceFunc(e ast.Expr, name string) *ast.FuncType {
	it, ok := e.(*ast.InterfaceType)
	if !ok {
		return nil
	}
	for _, m := range it.Methods.List {
		for _, n := range m.Names {
			if n.Name == name {
				ft, _ := m.Type.(*ast.FuncType)
				return ft
			}
		}
	}
	return nil
}

func (ix *index) anyExported(t *typ) bool {
	for m := range ix.methodSet(ref{t.pkg.Dir, t.Name}, map[string]bool{}) {
		if ast.IsExported(m) {
			return true
		}
	}
	return false
}

func (ix *index) importedDirs(p *pkg) []string {
	var out []string
	for _, d := range sortedKeys(p.imports) {
		if d != p.Dir && ix.pkgs[d] != nil {
			out = append(out, d)
		}
	}
	return out
}

func (ix *index) lookup(r ref) *typ {
	if p := ix.pkgs[r.dir]; p != nil {
		return p.types[r.name]
	}
	return nil
}

// methodSet maps the names of a type's methods to their arity, including
// methods promoted from embedded types and, for an interface, embedded
// interfaces.
func (ix *index) methodSet(r ref, seen map[string]bool) map[string]string {
	out := map[string]string{}
	t := ix.lookup(r)
	if t == nil || seen[r.id()] {
		return out
	}
	seen[r.id()] = true
	for name, m := range t.methods {
		out[name] = m.arity
	}
	for _, e := range t.embedded {
		for name, a := range ix.methodSet(e, seen) {
			if _, ok := out[name]; !ok {
				out[name] = a
			}
		}
	}
	return out
}

// method finds where a type's method is declared, following embedding.
func (ix *index) method(r ref, name string, seen map[string]bool) *fn {
	t := ix.lookup(r)
	if t == nil || seen[r.id()] {
		return nil
	}
	seen[r.id()] = true
	if m := t.methods[name]; m != nil {
		return m
	}
	for _, e := range t.embedded {
		if m := ix.method(e, name, seen); m != nil {
			return m
		}
	}
	return nil
}

// field returns the type a struct field refers to, following embedding.
func (ix *index) field(r ref, name string, seen map[string]bool) (ref, bool) {
	t := ix.lookup(r)
	if t == nil || seen[r.id()] {
		return ref{}, false
	}
	seen[r.id()] = true
	if f, ok := t.fields[name]; ok {
		return f, true
	}
	for _, e := range t.embedded {
		if f, ok := ix.field(e, name, seen); ok {
			return f, true
		}
	}
	return ref{}, false
}

// mainRef returns the named type an expression is about: T for T, *T,
// []T, map[K]T, and pkg.T.
func (ix *index) mainRef(p *pkg, f *file, e ast.Expr) (ref, bool) {
	switch e := e.(type) {
	case *ast.Ident:
		if p.types[e.Name] != nil {
			return ref{p.Dir, e.Name}, true
		}
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			if dir, ok := f.imports[x.Name]; ok {
				return ref{dir, e.Sel.Name}, true
			}
		}
	case *ast.StarExpr:
		return ix.mainRef(p, f, e.X)
	case *ast.ArrayType:
		return ix.mainRef(p, f, e.Elt)
	case *ast.MapType:
		return ix.mainRef(p, f, e.Value)
	case *ast.ChanType:
		return ix.mainRef(p, f, e.Value)
	case *ast.IndexExpr:
		return ix.mainRef(p, f, e.X)
	case *ast.IndexListExpr:
		return ix.mainRef(p, f, e.X)
	}
	return ref{}, false
}

// refs returns every repository type an expression mentions.
func (ix *index) refs(p *pkg, f *file, e ast.Expr) []ref {
	var out []ref
	seen := map[ref]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		var r ref
		switch n := n.(type) {
		case *ast.SelectorExpr:
			x, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			dir, ok := f.imports[x.Name]
			if !ok {
				return true
			}
			r = ref{dir, n.Sel.Name}
		case *ast.Ident:
			if p.types[n.Name] == nil {
				return true
			}
			r = ref{p.Dir, n.Name}
		default:
			return true
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
		return false
	})
	return out
}

func (ix *index) resultRefs(p *pkg, f *file, ft *ast.FuncType) []ref {
	if ft.Results == nil {
		return nil
	}
	var out []ref
	for _, r := range ft.Results.List {
		rr, _ := ix.mainRef(p, f, r.Type)
		n := max(1, len(r.Names))
		for range n {
			out = append(out, rr)
		}
	}
	return out
}

// collectCalls resolves the calls in the file's function bodies.
func (ix *index) collectCalls(p *pkg, f *file, af *ast.File) {
	for _, d := range af.Decls {
		fd, ok := d.(*ast.FuncDecl)
		// Generated code's calls are its plumbing.
		if !ok || fd.Body == nil || fd.Name.Name == "init" || p.Role == "generated" {
			continue
		}
		from := p.Dir + "#" + fd.Name.Name
		vars := map[string]ref{}
		if fd.Recv != nil && len(fd.Recv.List) == 1 {
			recv, _ := recvType(fd.Recv.List[0].Type)
			from = p.Dir + "#" + recv + "." + fd.Name.Name
			for _, n := range fd.Recv.List[0].Names {
				vars[n.Name] = ref{p.Dir, recv}
			}
		}
		for _, fl := range fd.Type.Params.List {
			if r, ok := ix.mainRef(p, f, fl.Type); ok {
				for _, n := range fl.Names {
					vars[n.Name] = r
				}
			}
		}
		seen := map[string]bool{}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				// x := T{...}, x := &T{...}, x := pkg.New(...)
				for i, lhs := range n.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || len(n.Rhs) == 0 {
						continue
					}
					if r, ok := ix.exprType(p, f, vars, n.Rhs, i); ok {
						vars[id.Name] = r
					}
				}
			case *ast.ValueSpec:
				if n.Type != nil {
					if r, ok := ix.mainRef(p, f, n.Type); ok {
						for _, id := range n.Names {
							vars[id.Name] = r
						}
					}
				}
			case *ast.CallExpr:
				if to := ix.callee(p, f, vars, n.Fun); to != nil && !seen[to.id] && !ix.plumbing(to) {
					seen[to.id] = true
					ix.calls = append(ix.calls, Call{From: from, To: to.id, Line: f.fset.Position(n.Pos()).Line})
				}
			}
			return true
		})
	}
}

// plumbing reports whether a call target is generated plumbing: a
// protobuf message's getters, or an unexported generated function.
func (ix *index) plumbing(x *fn) bool {
	if x.pkg.Role != "generated" {
		return false
	}
	if x.recv == "" {
		return !ast.IsExported(x.decl.Name.Name)
	}
	t := x.pkg.types[x.recv]
	return t == nil || t.message || !ast.IsExported(x.recv)
}

// arity describes a function type's shape: the number of parameters and
// results, and whether it's variadic.
func arity(ft *ast.FuncType) string {
	count := func(l *ast.FieldList) (n int, variadic bool) {
		if l == nil {
			return 0, false
		}
		for _, f := range l.List {
			n += max(1, len(f.Names))
			if _, ok := f.Type.(*ast.Ellipsis); ok {
				variadic = true
			}
		}
		return n, variadic
	}
	params, variadic := count(ft.Params)
	results, _ := count(ft.Results)
	return strconv.Itoa(params) + map[bool]string{true: "...", false: ""}[variadic] + "/" + strconv.Itoa(results)
}

// exprType returns the type of the i-th value an assignment's right-hand
// side produces, when it's evident without type checking.
func (ix *index) exprType(p *pkg, f *file, vars map[string]ref, rhs []ast.Expr, i int) (ref, bool) {
	if len(rhs) == 1 {
		if call, ok := rhs[0].(*ast.CallExpr); ok {
			if x := ix.callee(p, f, vars, call.Fun); x != nil && i < len(x.results) && x.results[i].name != "" {
				return x.results[i], true
			}
			return ref{}, false
		}
	}
	if i >= len(rhs) {
		return ref{}, false
	}
	e := rhs[i]
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	if cl, ok := e.(*ast.CompositeLit); ok && cl.Type != nil {
		return ix.mainRef(p, f, cl.Type)
	}
	return ref{}, false
}

// callee resolves the function or method a call expression calls.
func (ix *index) callee(p *pkg, f *file, vars map[string]ref, fun ast.Expr) *fn {
	switch e := fun.(type) {
	case *ast.Ident:
		if _, isVar := vars[e.Name]; !isVar {
			return p.funcs[e.Name]
		}
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			if r, ok := vars[x.Name]; ok {
				return ix.method(r, e.Sel.Name, map[string]bool{})
			}
			if dir, ok := f.imports[x.Name]; ok {
				if q := ix.pkgs[dir]; q != nil {
					return q.funcs[e.Sel.Name]
				}
			}
		case *ast.SelectorExpr:
			// v.field.Method()
			if v, ok := x.X.(*ast.Ident); ok {
				if r, ok := vars[v.Name]; ok {
					if fr, ok := ix.field(r, x.Sel.Name, map[string]bool{}); ok {
						return ix.method(fr, e.Sel.Name, map[string]bool{})
					}
				}
			}
		}
	}
	return nil
}

// model returns the view of one component.
func (ix *index) model(component string) *Model {
	m := &Model{
		Component: component, Packages: []Package{}, Neighbors: []Neighbor{}, Dependents: []Dependent{},
		Contracts: []Contract{}, Stubs: []Stub{}, Calls: []Call{},
	}
	mine := map[string]bool{}
	for _, dir := range sortedKeys(ix.pkgs) {
		if ix.pkgs[dir].component == component {
			mine[dir] = true
		}
	}
	neighbors := map[string]bool{}
	for _, dir := range sortedKeys(ix.pkgs) {
		p := ix.pkgs[dir]
		if !mine[dir] {
			for _, imp := range ix.importedDirs(p) {
				if mine[imp] {
					m.Dependents = append(m.Dependents, Dependent{From: dir, Component: p.component, To: imp})
				}
			}
			continue
		}
		out := p.Package
		out.Imports = []Import{}
		for _, imp := range ix.importedDirs(p) {
			q := ix.pkgs[imp]
			out.Imports = append(out.Imports, Import{Dir: imp, Component: q.component, Kind: ix.importKind(p, q)})
			if !mine[imp] && !neighbors[imp] {
				neighbors[imp] = true
				m.Neighbors = append(m.Neighbors, Neighbor{Dir: imp, Name: q.Name, Component: q.component, Kind: ix.importKind(p, q)})
			}
		}
		m.Packages = append(m.Packages, out)
	}
	// Stubs for the types outside the component that relations point to.
	stubs := map[string]bool{}
	for _, p := range m.Packages {
		for _, r := range p.Relations {
			dir, name, _ := strings.Cut(r.To, "#")
			if mine[dir] || stubs[r.To] {
				continue
			}
			t := ix.lookup(ref{dir, name})
			if t == nil {
				continue
			}
			stubs[r.To] = true
			s := Stub{ID: r.To, Name: name, Package: t.pkg.Name, Dir: dir, Component: t.pkg.component, Kind: t.Kind}
			if t.Kind == "interface" {
				for _, x := range sortedMethods(t) {
					if x.method.Exported {
						s.Methods = append(s.Methods, x.method.Name)
					}
				}
			}
			m.Stubs = append(m.Stubs, s)
		}
	}
	// Calls from or to the component, and the RPCs its calls make.
	reached := map[string]bool{}
	for _, c := range ix.calls {
		fd, _, _ := strings.Cut(c.From, "#")
		td, _, _ := strings.Cut(c.To, "#")
		if mine[fd] || mine[td] {
			m.Calls = append(m.Calls, c)
			if mine[fd] {
				reached[c.To] = true
			}
		}
	}
	for _, c := range ix.calls {
		fd, _, _ := strings.Cut(c.From, "#")
		td, _, _ := strings.Cut(c.To, "#")
		if c.Kind == "rpc" && reached[c.From] && !mine[fd] && !mine[td] {
			m.Calls = append(m.Calls, c)
		}
	}
	m.Dirs = map[string]Dir{}
	for _, c := range m.Calls {
		for _, id := range []string{c.From, c.To} {
			dir, _, _ := strings.Cut(id, "#")
			if p := ix.pkgs[dir]; p != nil {
				m.Dirs[dir] = Dir{Name: p.Name, Component: p.component}
			}
		}
	}
	return m
}

func (ix *index) importKind(from, to *pkg) string {
	switch {
	case from.component == to.component:
		return "same"
	case to.contract:
		return "contract"
	case ix.comps[to.component] != nil && ix.comps[to.component].Kind == "library":
		return "library"
	}
	return "other"
}

// recvType returns the type name of a method receiver and whether it's a
// pointer.
func recvType(e ast.Expr) (string, bool) {
	ptr := false
	if s, ok := e.(*ast.StarExpr); ok {
		e, ptr = s.X, true
	}
	switch x := e.(type) {
	case *ast.IndexExpr:
		e = x.X
	case *ast.IndexListExpr:
		e = x.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name, ptr
	}
	return "", ptr
}

func (f *file) pos(p token.Pos) Pos {
	return Pos{File: f.path, Line: f.fset.Position(p).Line}
}

func (f *file) print(e ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, f.fset, e)
	return b.String()
}

// sig prints a function type without its "func" keyword.
func (f *file) sig(ft *ast.FuncType) string {
	return strings.TrimPrefix(f.print(ft), "func")
}

func (f *file) source(from, to token.Pos) string {
	a, b := f.fset.Position(from).Offset, f.fset.Position(to).Offset
	if a < 0 || b > len(f.src) || a > b {
		return ""
	}
	lines := strings.Split(string(f.src[a:b]), "\n")
	if len(lines) > maxSourceLines {
		lines = append(lines[:maxSourceLines], "\t// …")
	}
	return strings.Join(lines, "\n")
}

func text(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return strings.TrimSpace(g.Text())
}

func uniqueSorted(s []string) []string {
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
