package terminal

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-08-BUF-01: RingBuffer normal read/write.
func TestRingBuffer_NormalReadWrite(t *testing.T) {
	rb := NewRingBuffer(64)

	// Write some data.
	n, err := rb.Write([]byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, 11, n)
	assert.Equal(t, 11, rb.Len())
	assert.False(t, rb.IsFull())

	// Read it back.
	data := rb.Read()
	assert.Equal(t, []byte("hello world"), data)

	// Write more data.
	n, err = rb.Write([]byte(" again"))
	require.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, 17, rb.Len())

	data = rb.Read()
	assert.Equal(t, []byte("hello world again"), data)
}

// TC-08-BUF-02: RingBuffer overflow FIFO — oldest data is discarded.
func TestRingBuffer_OverflowFIFO(t *testing.T) {
	rb := NewRingBuffer(10)

	// Write 10 bytes — fills buffer exactly.
	rb.Write([]byte("0123456789"))
	assert.Equal(t, 10, rb.Len())
	assert.True(t, rb.IsFull())

	// Write 5 more bytes — oldest 5 should be overwritten.
	rb.Write([]byte("ABCDE"))
	assert.Equal(t, 10, rb.Len())
	assert.True(t, rb.IsFull())

	data := rb.Read()
	assert.Equal(t, "56789ABCDE", string(data), "should contain last 10 bytes")

	// Write data larger than capacity — only last 10 bytes survive.
	rb.Write([]byte("this is a very long string that exceeds capacity"))
	data = rb.Read()
	assert.Equal(t, 10, len(data))
	assert.Equal(t, "s capacity", string(data), "should contain last 10 chars of the input")
}

// TC-08-BUF-03: RingBuffer empty replay — Read returns empty on fresh buffer.
func TestRingBuffer_EmptyReplay(t *testing.T) {
	rb := NewRingBuffer(64)

	data := rb.Read()
	assert.Empty(t, data)
	assert.Equal(t, 0, rb.Len())
	assert.False(t, rb.IsFull())

	// Write empty data.
	n, err := rb.Write(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, rb.Len())

	n, err = rb.Write([]byte{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 0, rb.Len())

	// Reset and verify.
	rb.Write([]byte("data"))
	assert.Equal(t, 4, rb.Len())
	rb.Reset()
	assert.Equal(t, 0, rb.Len())
	assert.False(t, rb.IsFull())
	data = rb.Read()
	assert.Empty(t, data)
}

// TC-08-BUF-04: RingBuffer concurrent safety with -race.
func TestRingBuffer_ConcurrentSafety(t *testing.T) {
	rb := NewRingBuffer(1024)

	var wg sync.WaitGroup
	// 10 concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rb.Write([]byte("data from goroutine"))
			}
		}(i)
	}

	// 5 concurrent readers.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = rb.Read()
				_ = rb.Len()
				_ = rb.IsFull()
			}
		}()
	}

	wg.Wait()

	// No panic, no data race — the test passes if -race doesn't complain.
	assert.True(t, rb.Len() > 0)
}

// Seq is the marker every derived cache keys on (see sessions_overview.go). Its whole job is that
// "same value ⟹ same bytes" holds in BOTH directions, so each way it can break gets a case.
func TestRingBuffer_SeqTracksContentIdentity(t *testing.T) {
	rb := NewRingBuffer(16)

	start := rb.Seq()
	assert.Equal(t, start, rb.Seq(), "reading must not move the marker")

	rb.Write([]byte("abc"))
	afterWrite := rb.Seq()
	assert.NotEqual(t, start, afterWrite, "a write that changed the content left the marker still")

	// A zero-length write changes nothing, so neither may the marker — otherwise every idle tick
	// that happens to flush an empty buffer would invalidate a perfectly good cached screen.
	rb.Write(nil)
	assert.Equal(t, afterWrite, rb.Seq(), "an empty write moved the marker")

	// Counting BYTES, not calls: two same-length writes must be distinguishable from one.
	rb.Write([]byte("de"))
	twoWrites := rb.Seq()
	rb.Write([]byte("fg"))
	assert.NotEqual(t, twoWrites, rb.Seq(), "a second same-length write was indistinguishable from the first")

	// Wrapping past capacity must keep moving it. writePos returns to values it has held before —
	// that is exactly why the marker cannot be derived from writePos or from Len().
	beforeWrap := rb.Seq()
	rb.Write([]byte("0123456789abcdefghij"))
	assert.True(t, rb.Seq() > beforeWrap, "a wrapping write did not move the marker forward")

	// Reset is the one content change that writes no byte. Missed here, a cache would keep serving
	// the screen of a buffer that has been cleared.
	beforeReset := rb.Seq()
	rb.Reset()
	assert.NotEqual(t, beforeReset, rb.Seq(), "Reset emptied the buffer without moving the marker")
	assert.Equal(t, 0, rb.Len())
}
