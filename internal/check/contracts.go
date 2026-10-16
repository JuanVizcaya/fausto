package check

import (
	"bufio"
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/spec"
)

const protoDir = "proto"

// checkContracts verifies that each declared contract has proto files whose
// package matches the versioned directory, that generated code exists, and
// that every proto package in the repository is declared.
func checkContracts(r *Repo) []Finding {
	var out []Finding
	declared := map[string]bool{}
	for _, c := range r.Spec.System.Components {
		for _, ct := range c.Contracts {
			declared[ct.Proto] = true
			protos := 0
			for _, f := range r.filesUnder(ct.Proto) {
				if !strings.HasSuffix(f, ".proto") || path.Dir(f) != ct.Proto {
					continue
				}
				protos++
				want := strings.ReplaceAll(strings.TrimPrefix(ct.Proto, protoDir+"/"), "/", ".")
				got, err := r.protoPackage(f)
				switch {
				case err != nil:
					out = append(out, Finding{Message: err.Error(), Component: c.ID, Path: f})
				case got != want:
					out = append(out, Finding{Message: fmt.Sprintf("proto package %q does not match its directory (want %q)", got, want), Component: c.ID, Path: f})
				}
			}
			if protos == 0 && c.Status == "active" {
				out = append(out, Finding{Message: "declared contract has no .proto files", Component: c.ID, Path: ct.Proto})
			}
			if ct.Generated == "" {
				continue
			}
			generated := 0
			for _, f := range r.filesUnder(ct.Generated) {
				if strings.HasSuffix(f, ".pb.go") {
					generated++
				}
			}
			if generated == 0 && protos > 0 {
				out = append(out, Finding{Message: "no generated .pb.go files; run buf generate", Component: c.ID, Path: ct.Generated})
			}
		}
	}
	// Active events need their message in the producer's contract.
	for _, e := range r.Spec.Events.Events {
		dir, message := e.ProtoDir(r.Spec.Layout.ProtoRoot)
		if e.Status != "active" || !declared[dir] {
			continue // undeclared packages are reported by the schema check
		}
		def := regexp.MustCompile(`(?m)^\s*message\s+` + regexp.QuoteMeta(message) + `\s*\{`)
		found := false
		for _, f := range r.filesUnder(dir) {
			if data, err := r.read(f); err == nil && strings.HasSuffix(f, ".proto") && def.Match(data) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, Finding{Message: fmt.Sprintf("event %q: message %s is not defined in %s", e.ID, message, dir), Component: e.Producer, Path: dir})
		}
	}

	reported := map[string]bool{}
	for _, f := range r.filesUnder(protoDir) {
		dir := path.Dir(f)
		if !strings.HasSuffix(f, ".proto") || declared[dir] || reported[dir] {
			continue
		}
		reported[dir] = true
		out = append(out, Finding{Message: fmt.Sprintf("proto package %s is not declared as a contract of any component in %s/system.yaml", dir, spec.Dir), Path: dir})
	}
	return out
}

func (r *Repo) protoPackage(file string) (string, error) {
	data, err := r.read(file)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if pkg, ok := strings.CutPrefix(line, "package "); ok {
			return strings.TrimSpace(strings.TrimSuffix(pkg, ";")), nil
		}
	}
	return "", fmt.Errorf("no package statement")
}
