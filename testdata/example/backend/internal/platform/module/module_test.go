package module

import (
	"errors"
	"slices"
	"testing"
)

type greeter interface{ Greet() string }

type english struct{}

func (english) Greet() string { return "hello" }

type fakeModule struct {
	name     string
	register func(*Registry) error
}

func (m fakeModule) Name() string { return m.name }

func (m fakeModule) Register(r *Registry) error {
	if m.register == nil {
		return nil
	}
	return m.register(r)
}

func TestAddAndLookup(t *testing.T) {
	r := NewRegistry()
	err := r.Add(fakeModule{name: "greetings", register: func(r *Registry) error {
		return Provide[greeter](r, english{})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(fakeModule{name: "audit"}); err != nil {
		t.Fatal(err)
	}
	if got := r.Names(); !slices.Equal(got, []string{"greetings", "audit"}) {
		t.Fatalf("Names() = %v", got)
	}
	g, err := Lookup[greeter](r)
	if err != nil {
		t.Fatal(err)
	}
	if g.Greet() != "hello" {
		t.Fatalf("Greet() = %q", g.Greet())
	}
}

func TestAddRejectsInvalidAndDuplicateNames(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"", "Finance", "access-control", "1st"} {
		if err := r.Add(fakeModule{name: name}); err == nil {
			t.Errorf("Add accepted name %q", name)
		}
	}
	if err := r.Add(fakeModule{name: "finance"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(fakeModule{name: "finance"}); err == nil {
		t.Fatal("Add accepted a duplicate name")
	}
}

func TestAddReturnsRegisterError(t *testing.T) {
	want := errors.New("boom")
	err := NewRegistry().Add(fakeModule{name: "finance", register: func(*Registry) error { return want }})
	if !errors.Is(err, want) {
		t.Fatalf("Add error = %v, want %v", err, want)
	}
}

func TestProvideRules(t *testing.T) {
	r := NewRegistry()
	if err := Provide[english](r, english{}); err == nil {
		t.Error("Provide accepted a non-interface contract")
	}
	if err := Provide[greeter](r, nil); err == nil {
		t.Error("Provide accepted a nil implementation")
	}
	if err := Provide[greeter](r, english{}); err != nil {
		t.Fatal(err)
	}
	if err := Provide[greeter](r, english{}); err == nil {
		t.Error("Provide accepted a second implementation")
	}
}

func TestLookupNotProvided(t *testing.T) {
	if _, err := Lookup[greeter](NewRegistry()); !errors.Is(err, ErrNotProvided) {
		t.Fatalf("Lookup error = %v, want ErrNotProvided", err)
	}
}
