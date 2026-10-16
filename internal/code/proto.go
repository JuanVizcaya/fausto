package code

import (
	"path"
	"regexp"
	"strings"

	"github.com/JuanVizcaya/fausto/internal/check"
)

var (
	protoService = regexp.MustCompile(`^service\s+(\w+)`)
	protoRPC     = regexp.MustCompile(`^rpc\s+(\w+)\s*\(\s*(stream\s+)?([\w.]+)\s*\)\s*returns\s*\(\s*(stream\s+)?([\w.]+)\s*\)`)
	protoMessage = regexp.MustCompile(`^message\s+(\w+)`)
)

// readContract lists the services, RPCs, and messages of the .proto files
// in dir. It reads declarations line by line, which is enough for the
// one-declaration-per-line style buf format produces.
func readContract(repo *check.Repo, dir, generated string) Contract {
	ct := Contract{Proto: dir, Generated: generated, Services: []Service{}, Messages: []string{}}
	for _, f := range repo.Files {
		if path.Dir(f) != dir || !strings.HasSuffix(f, ".proto") {
			continue
		}
		src, err := repo.Read(f)
		if err != nil {
			continue
		}
		var svc *Service
		depth := 0
		for _, line := range strings.Split(string(src), "\n") {
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			line = strings.TrimSpace(line)
			switch {
			case depth == 0 && protoService.MatchString(line):
				ct.Services = append(ct.Services, Service{Name: protoService.FindStringSubmatch(line)[1], RPCs: []RPC{}})
				svc = &ct.Services[len(ct.Services)-1]
			case depth == 1 && svc != nil && protoRPC.MatchString(line):
				m := protoRPC.FindStringSubmatch(line)
				svc.RPCs = append(svc.RPCs, RPC{Name: m[1], Request: m[3], Response: m[5], ClientStreams: m[2] != "", ServerStreams: m[4] != ""})
			case depth == 0 && protoMessage.MatchString(line):
				ct.Messages = append(ct.Messages, protoMessage.FindStringSubmatch(line)[1])
			}
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth == 0 {
				svc = nil
			}
		}
	}
	return ct
}
