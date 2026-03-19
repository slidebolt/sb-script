package app

import "testing"

func TestHelloManifest(t *testing.T) {
	h := New().Hello()
	if h.ID != ServiceID {
		t.Fatalf("id: got %q want %q", h.ID, ServiceID)
	}
	if len(h.DependsOn) != 2 || h.DependsOn[0] != "messenger" || h.DependsOn[1] != "storage" {
		t.Fatalf("dependsOn: got %v want [messenger storage]", h.DependsOn)
	}
}
