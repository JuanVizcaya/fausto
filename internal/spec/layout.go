package spec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LayoutFile is the optional file, at the repository root, that tells
// fausto where a project keeps what the checks look at, when that differs
// from DefaultLayout. A project without it needs nothing from fausto.
const LayoutFile = ".fausto.yaml"

// Layout is where a project keeps its code, as the checks see it.
type Layout struct {
	// CommandsDir holds one directory per Go command (a service's main).
	CommandsDir string `yaml:"commands_dir,omitempty" json:"commandsDir"`
	// ComposeFile is the Docker Compose file that runs the system; the
	// files it includes are read too.
	ComposeFile string `yaml:"compose_file,omitempty" json:"composeFile"`
	// ProtoRoot is where protobuf packages live, one directory per package
	// segment (habyta.residents.v1 in <proto_root>/habyta/residents/v1).
	ProtoRoot string `yaml:"proto_root,omitempty" json:"protoRoot"`
}

// DefaultLayout is the layout of a project without a LayoutFile.
var DefaultLayout = Layout{CommandsDir: "backend/cmd", ComposeFile: "compose.yaml", ProtoRoot: "proto"}

// LoadLayout reads root's LayoutFile, filling what it leaves out from
// DefaultLayout.
func LoadLayout(root string) (Layout, error) {
	l := DefaultLayout
	data, err := os.ReadFile(filepath.Join(root, LayoutFile))
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	var f struct {
		Layout `yaml:",inline"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return l, fmt.Errorf("%s: %w", LayoutFile, err)
	}
	clean := func(dst *string, v string) {
		if v != "" {
			*dst = filepath.ToSlash(filepath.Clean(v))
		}
	}
	clean(&l.CommandsDir, f.CommandsDir)
	clean(&l.ComposeFile, f.ComposeFile)
	clean(&l.ProtoRoot, f.ProtoRoot)
	return l, nil
}
