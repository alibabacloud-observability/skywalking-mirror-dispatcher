package policy

import "testing"

func TestMethodMatrix(t *testing.T) {
	if len(Methods) != 29 {
		t.Fatalf("method count = %d, want 29", len(Methods))
	}
	var mirrored, oapOnly int
	for method, route := range Methods {
		if method == "" {
			t.Fatal("empty method")
		}
		switch route {
		case MirrorToARMS:
			mirrored++
		case OAPOnly:
			oapOnly++
		default:
			t.Fatalf("unknown route %d for %s", route, method)
		}
	}
	if mirrored != 20 || oapOnly != 9 {
		t.Fatalf("route counts = %d mirrored, %d OAP-only; want 20/9", mirrored, oapOnly)
	}
}
