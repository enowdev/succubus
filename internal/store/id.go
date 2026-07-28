package store

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"sync"
	"time"
)

// Crockford base32, the ULID alphabet.
const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu   sync.Mutex
	lastMS   int64
	lastRand [10]byte
)

// NewID returns a lexicographically sortable ULID-style identifier. Sortability
// matters: it gives tasks and plans a stable natural creation order without a
// separate sequence column.
func NewID() string {
	ulidMu.Lock()
	defer ulidMu.Unlock()

	ms := time.Now().UnixMilli()
	if ms == lastMS {
		// Same millisecond: increment the random component so IDs stay unique
		// and monotonic within the millisecond.
		for i := len(lastRand) - 1; i >= 0; i-- {
			lastRand[i]++
			if lastRand[i] != 0 {
				break
			}
		}
	} else {
		lastMS = ms
		rand.Read(lastRand[:])
	}

	var buf [16]byte
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(ms))
	copy(buf[0:6], t[2:8]) // 48-bit timestamp
	copy(buf[6:16], lastRand[:])

	var sb strings.Builder
	sb.Grow(26)
	// Encode 128 bits as 26 base32 chars (130 bits, top 2 padded).
	var acc uint16
	bits := 0
	out := make([]byte, 0, 26)
	for _, b := range buf {
		acc = acc<<8 | uint16(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, ulidAlphabet[(acc>>uint(bits))&0x1f])
		}
	}
	if bits > 0 {
		out = append(out, ulidAlphabet[(acc<<uint(5-bits))&0x1f])
	}
	for len(out) < 26 {
		out = append(out, '0')
	}
	sb.Write(out[:26])
	return sb.String()
}
