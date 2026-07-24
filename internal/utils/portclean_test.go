package utils

import (
	"net"
	"testing"
	"time"
)

func TestEnsurePortFreeDoesNotTakeOverOccupiedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	if err := EnsurePortFree(port, nil); err == nil {
		t.Fatalf("expected occupied port %d to be rejected", port)
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("occupied listener on port %d was disrupted: %v", port, err)
	}
	_ = connection.Close()
}
