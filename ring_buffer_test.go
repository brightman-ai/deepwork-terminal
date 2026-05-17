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
