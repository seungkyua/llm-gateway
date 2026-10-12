package main

import (
	"strings"
	"testing"
)

func TestNewRequestIDShouldStartWithReqPrefix(t *testing.T) {
	id := newRequestID()

	if !strings.HasPrefix(id, "req_") {
		t.Errorf("expected id to start with req_, got %q", id)
	}
}

func TestNewRequestIDShouldReturnDifferentValuesOnEachCall(t *testing.T) {
	id1 := newRequestID()
	id2 := newRequestID()

	if id1 == id2 {
		t.Error("expected distinct request ids across calls")
	}
}
