package core

import (
	"context"
	"net/http"
	"testing"
)

type topoTestModule struct {
	name string
	deps []string
}

func (f *topoTestModule) Name() string               { return f.name }
func (f *topoTestModule) Dependencies() []string      { return f.deps }
func (f *topoTestModule) Init(_ *Core) error          { return nil }
func (f *topoTestModule) RegisterRoutes(_ *http.ServeMux) {}
func (f *topoTestModule) Start(_ context.Context) error { return nil }
func (f *topoTestModule) Stop(_ context.Context) error  { return nil }

func TestTopoSort_BasicOrder(t *testing.T) {
	modules := []Module{
		&topoTestModule{name: "stream", deps: []string{"session", "cc"}},
		&topoTestModule{name: "cc", deps: []string{"session"}},
		&topoTestModule{name: "session", deps: nil},
	}
	sorted, err := topoSort(modules)
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, m := range sorted {
		idx[m.Name()] = i
	}
	if idx["session"] >= idx["cc"] {
		t.Errorf("session (%d) should come before cc (%d)", idx["session"], idx["cc"])
	}
	if idx["cc"] >= idx["stream"] {
		t.Errorf("cc (%d) should come before stream (%d)", idx["cc"], idx["stream"])
	}
}

func TestTopoSort_CycleDetection(t *testing.T) {
	modules := []Module{
		&topoTestModule{name: "a", deps: []string{"b"}},
		&topoTestModule{name: "b", deps: []string{"a"}},
	}
	_, err := topoSort(modules)
	if err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestTopoSort_UnknownDependency(t *testing.T) {
	modules := []Module{
		&topoTestModule{name: "a", deps: []string{"nonexistent"}},
	}
	_, err := topoSort(modules)
	if err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestTopoSort_DuplicateName(t *testing.T) {
	modules := []Module{
		&topoTestModule{name: "a", deps: nil},
		&topoTestModule{name: "a", deps: nil},
	}
	_, err := topoSort(modules)
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestTopoSort_NoDeps(t *testing.T) {
	modules := []Module{
		&topoTestModule{name: "a", deps: nil},
		&topoTestModule{name: "b", deps: nil},
	}
	sorted, err := topoSort(modules)
	if err != nil {
		t.Fatal(err)
	}
	if len(sorted) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(sorted))
	}
	// Stable order: should match original slice order
	if sorted[0].Name() != "a" || sorted[1].Name() != "b" {
		t.Errorf("expected stable order [a, b], got [%s, %s]", sorted[0].Name(), sorted[1].Name())
	}
}
