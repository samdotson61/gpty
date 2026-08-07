//go:build live

package mesh

// Live smoke for the mesh primitives against a REAL tmux server — validates
// the parts fakes can't: the <-escaping through the keys grammar, paste
// delivery, and WaitFor marker matching. Spawns and kills only its own
// sessions; unlike the conformance suite it never touches kill-server, so it
// is safe to run on a box with live sessions:
//
//	go test -tags live -run Live ./internal/mesh
import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/session"
)

func TestLiveSendWithDoneRoundTrip(t *testing.T) {
	eng := engine.Exec{}
	name := fmt.Sprintf("meshsmoke-%d", os.Getpid())
	if err := eng.Spawn(name, "", "", 100, 30); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = session.Kill(name) }()

	// Wait for a shell prompt (pwsh "...>" or POSIX "$").
	if _, err := eng.WaitFor(name, `[$>]`, 15); err != nil {
		t.Fatalf("no shell prompt: %v", err)
	}

	// Both pwsh and POSIX shells accept this line verbatim.
	reply, err := SendWithDone(eng, name, "echo 'reply text'; echo '<<END>>'\n", "", 15)
	if err != nil {
		t.Fatalf("SendWithDone: %v", err)
	}
	if reply != "reply text" {
		t.Errorf("reply = %q, want %q", reply, "reply text")
	}

	since, err := SnapshotSince(eng, name, "reply text")
	if err != nil {
		t.Fatalf("SnapshotSince: %v", err)
	}
	if !strings.Contains(since, "<<END>>") {
		t.Errorf("SnapshotSince after anchor lost the marker: %q", since)
	}
}
