//go:build live

package conformance

import (
	"fmt"
	"testing"

	"github.com/samdotson61/gpty/internal/ctl"
	"github.com/samdotson61/gpty/internal/session"
)

// These benchmarks produce the build-plan §6 latency numbers on the host they
// run on. The headline comparison: exec one-shot (a process spawn per call) vs
// the resident control-mode channel (a pipe round-trip per call).
//
//	go test -tags live -bench . -benchmem ./conformance/...

func benchSession(b *testing.B) string {
	b.Helper()
	name := fmt.Sprintf("bench-%d", b.N)
	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		b.Fatalf("Spawn: %v", err)
	}
	b.Cleanup(func() { _ = session.Kill(name) })
	return name
}

func BenchmarkExecSnapshot(b *testing.B) {
	name := benchSession(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := session.Snapshot(name); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCtlSnapshot(b *testing.B) {
	name := benchSession(b)
	client, err := ctl.Dial()
	if err != nil {
		b.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	target := session.Full(name)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.Capture(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecSend(b *testing.B) {
	name := benchSession(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := session.Send(name, "x"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCtlSend(b *testing.B) {
	name := benchSession(b)
	client, err := ctl.Dial()
	if err != nil {
		b.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	target := session.Full(name)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := client.Send(target, "x"); err != nil {
			b.Fatal(err)
		}
	}
}
