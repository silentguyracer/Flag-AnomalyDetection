package ringbuf

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRingBuffer_Sequential(t *testing.T) {
	rb := NewRingBuffer[int](4)
	assert.Equal(t, 4, rb.Capacity())

	// Push 4 items
	for i := 1; i <= 4; i++ {
		err := rb.Offer(i * 10)
		require.NoError(t, err)
	}

	// 5th item should fail (buffer full)
	err := rb.Offer(50)
	assert.ErrorIs(t, err, ErrBufferFull)

	// Consume items
	for i := 1; i <= 4; i++ {
		val, err := rb.Poll()
		require.NoError(t, err)
		assert.Equal(t, i*10, val)
	}

	// Empty buffer should return ErrBufferEmpty
	_, err = rb.Poll()
	assert.ErrorIs(t, err, ErrBufferEmpty)
}

func TestRingBuffer_HighThroughputConcurrent(t *testing.T) {
	rb := NewRingBuffer[int64](1024)
	numItems := 50000

	var wg sync.WaitGroup
	wg.Add(2)

	// Producer
	go func() {
		defer wg.Done()
		for i := 0; i < numItems; i++ {
			for {
				if err := rb.Offer(int64(i)); err == nil {
					break
				}
			}
		}
	}()

	// Consumer
	var consumedCount int
	go func() {
		defer wg.Done()
		for consumedCount < numItems {
			if _, err := rb.Poll(); err == nil {
				consumedCount++
			}
		}
	}()

	wg.Wait()
	assert.Equal(t, numItems, consumedCount)
}
