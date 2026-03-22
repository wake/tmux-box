package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type testService struct{ Name string }

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewServiceRegistry()
	svc := &testService{Name: "hello"}
	r.Register("test.service", svc)
	got, ok := r.Get("test.service")
	assert.True(t, ok)
	assert.Equal(t, svc, got.(*testService))
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewServiceRegistry()
	got, ok := r.Get("nonexistent")
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestRegistryMustGet(t *testing.T) {
	r := NewServiceRegistry()
	assert.Panics(t, func() { r.MustGet("nonexistent") })
	r.Register("x", &testService{})
	assert.NotPanics(t, func() { r.MustGet("x") })
}
