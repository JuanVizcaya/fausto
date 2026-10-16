package check

import (
	"fmt"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// checkLayers parses the imports of every Go file that belongs to a
// component and verifies each import of another component:
//   - a generated contract needs a grpc dependency on the contract's owner;
//   - a library's code needs a library dependency on it;
//   - any other component's code may not be imported at all.
//
// Layer rules for the declared dependencies themselves are checked by the
// schema check.
func checkLayers(r *Repo) []Finding {
	var out []Finding
	resolve, err := r.GoImportResolver()
	if err != nil {
		out = append(out, Finding{Message: err.Error()})
	}

	fset := token.NewFileSet()
	for _, f := range r.Files {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		from := r.componentOf(f)
		if from == nil {
			continue
		}
		src, err := r.read(f)
		if err != nil {
			out = append(out, Finding{Message: err.Error(), Component: from.ID, Path: f})
			continue
		}
		file, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			out = append(out, Finding{Message: "parse: " + err.Error(), Component: from.ID, Path: f})
			continue
		}
		for _, spec := range file.Imports {
			imp, _ := strconv.Unquote(spec.Path.Value)
			dir, ok := resolve(imp)
			if !ok {
				continue // standard library or third party
			}
			if owner, ct := r.contractOwner(dir); owner != nil {
				switch {
				case owner.ID == from.ID:
				case r.isEventContract(owner, ct):
					if !r.Spec.Consumes(from.ID, owner.ID) {
						out = append(out, Finding{
							Message:   fmt.Sprintf("imports the %q events contract (%s) without consuming one of its events in events.yaml", owner.ID, imp),
							Component: from.ID, Path: f,
						})
					}
				case !from.Uses(owner.ID, "grpc"):
					out = append(out, Finding{
						Message:   fmt.Sprintf("imports the %q contract (%s) without declaring a grpc dependency on %q", owner.ID, imp, owner.ID),
						Component: from.ID, Path: f,
					})
				}
				continue
			}
			to := r.componentOf(dir)
			switch {
			case to == nil:
				out = append(out, Finding{Message: fmt.Sprintf("imports %s, which belongs to no declared component", imp), Component: from.ID, Path: f})
			case to.ID == from.ID:
			case to.Kind == "library":
				if !from.Uses(to.ID, "library") {
					out = append(out, Finding{
						Message:   fmt.Sprintf("imports library %q (%s) without declaring a library dependency on it", to.ID, imp),
						Component: from.ID, Path: f,
					})
				}
			default:
				out = append(out, Finding{
					Message:   fmt.Sprintf("imports internal code of %s %q (%s); call it through its contract instead", to.Kind, to.ID, imp),
					Component: from.ID, Path: f,
				})
			}
		}
	}
	return out
}
