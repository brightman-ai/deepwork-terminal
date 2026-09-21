package terminal

import (
	"github.com/brightman-ai/deepwork-terminal/ansisignal"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestoringHistoryDoesNotRefireSignals(t *testing.T) {
	sm := newRealPTYManager(t, 1<<16, "/bin/sh")
	sess, err := sm.Create("replay-signal")
	require.NoError(t, err)
	// Actual OSC notification bytes are produced only inside this isolated test PTY.
	require.NoError(t, sess.WriteInput([]byte("printf '\\033]9;old notice\\007'; echo OLD-NOTICE-DONE\n")))
	waitForBufferContains(t, sess, "OLD-NOTICE-DONE", 5*time.Second)
	var count atomic.Int32
	sm.OnSignal = func(s *Session, _ ansisignal.Signal) {
		if s != sess {
			count.Add(1)
		}
	}
	sm.sessions.Delete(sess.ID)
	require.NoError(t, sm.Restore())
	restored := sm.List()[0]
	waitForBufferContains(t, restored, "old notice", 5*time.Second)
	require.Eventually(t, func() bool { return restored.Buffer.Len() >= sess.Buffer.Len() }, time.Second, 5*time.Millisecond)
	require.Equal(t, int32(0), count.Load(), "restored history must not become a new notification")
	require.NoError(t, restored.WriteInput([]byte("printf '\\033]9;new notice\\007'\n")))
	require.Eventually(t, func() bool { return count.Load() == 1 }, time.Second, 5*time.Millisecond)
}
