package secure

import (
	"context"
	"testing"
)

func TestPublicPolicyRejectsLoopback(t *testing.T) {
	if err := ValidatePublicTarget(context.Background(), "http://127.0.0.1:8080", false); err == nil {
		t.Fatal("loopback URL accepted")
	}
	if conn, err := PublicDialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		conn.Close()
		t.Fatal("restricted dialer accepted loopback")
	}
	if err := ValidatePublicTarget(context.Background(), "http://127.0.0.1:8080", true); err != nil {
		t.Fatal(err)
	}
}
