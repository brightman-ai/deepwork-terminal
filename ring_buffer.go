package terminal

import "sync"

// DefaultBufferCapacity is the default size for the RingBuffer (1 MB).
// [Ref: T5-B3, CAP-terminal-io S4, DDC-03]
const DefaultBufferCapacity = 1024 * 1024

// RingBuffer is a fixed-capacity circular byte buffer used to store recent
// terminal output for replay on WebSocket reconnection.
// It is safe for concurrent use.
// [Ref: T5-B3, CAP-terminal-io S4, DDC-03]
// Testability: Len() int + IsFull() bool [Ref: T6-A4.1]
type RingBuffer struct {
	data     []byte
	capacity int
	writePos int
	full     bool
	mu       sync.Mutex
}

// NewRingBuffer creates a RingBuffer with the given capacity.
// If capacity <= 0, DefaultBufferCapacity is used.
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	return &RingBuffer{
		data:     make([]byte, capacity),
		capacity: capacity,
	}
}

// Write appends data to the ring buffer, overwriting oldest data when full (FIFO).
// Returns the number of bytes written (always len(p)).
func (rb *RingBuffer) Write(p []byte) (int, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}

	// If input is larger than capacity, only keep the last capacity bytes.
	if n >= rb.capacity {
		copy(rb.data, p[n-rb.capacity:])
		rb.writePos = 0
		rb.full = true
		return n, nil
	}

	// How many bytes fit before wrapping?
	remaining := rb.capacity - rb.writePos
	if n <= remaining {
		copy(rb.data[rb.writePos:], p)
	} else {
		copy(rb.data[rb.writePos:], p[:remaining])
		copy(rb.data, p[remaining:])
	}

	newPos := (rb.writePos + n) % rb.capacity
	if !rb.full && (newPos < rb.writePos || newPos == 0 && n > 0 && rb.writePos+n >= rb.capacity) {
		rb.full = true
	}
	rb.writePos = newPos

	return n, nil
}

// Read returns all buffered data in chronological order.
// If the buffer has never wrapped, returns data from the start to writePos.
// If wrapped, returns data from writePos to end + start to writePos.
func (rb *RingBuffer) Read() []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.full {
		// Buffer hasn't wrapped yet.
		result := make([]byte, rb.writePos)
		copy(result, rb.data[:rb.writePos])
		return result
	}

	// Buffer is full/wrapped: read from writePos to end, then start to writePos.
	result := make([]byte, rb.capacity)
	n := copy(result, rb.data[rb.writePos:])
	copy(result[n:], rb.data[:rb.writePos])
	return result
}

// ReadTail returns the last n bytes from the buffer without copying the entire buffer.
// This minimizes mutex hold time — critical for avoiding PTY input backpressure.
func (rb *RingBuffer) ReadTail(n int) []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	total := rb.writePos
	if rb.full {
		total = rb.capacity
	}
	if total == 0 {
		return nil
	}
	if n > total {
		n = total
	}

	result := make([]byte, n)
	if !rb.full {
		// Linear: just copy the tail of [0..writePos).
		copy(result, rb.data[rb.writePos-n:rb.writePos])
	} else {
		// Wrapped: logical end is at writePos.
		// Last n bytes = going backwards from writePos in circular buffer.
		start := rb.writePos - n
		if start >= 0 {
			copy(result, rb.data[start:rb.writePos])
		} else {
			// Wraps around: take from end of buffer + beginning.
			tailLen := -start // bytes from the end
			copy(result, rb.data[rb.capacity+start:])
			copy(result[tailLen:], rb.data[:rb.writePos])
		}
	}
	return result
}

// Len returns the number of valid bytes currently stored.
// [Ref: T6-A4.1 testability requirement]
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.full {
		return rb.capacity
	}
	return rb.writePos
}

// IsFull returns true if the buffer has been completely filled at least once.
// [Ref: T6-A4.1 testability requirement]
func (rb *RingBuffer) IsFull() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.full
}

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.writePos = 0
	rb.full = false
}
