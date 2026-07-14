// Package rng is a tiny, fully-specified deterministic pseudo-random
// generator. hookstorm builds its delivery plans from a seed, and the whole
// point is that the same seed reproduces a byte-identical storm on every
// machine and every Go version — so we cannot lean on math/rand, whose
// algorithm is an implementation detail that has changed across releases.
//
// The generator is splitmix64 (Steele, Lea & Flood 2014): a single 64-bit
// state, one multiply-xorshift finalizer, excellent statistical quality for
// a stream generator, and trivial to reimplement in any language if the
// plan format is ever ported.
package rng

// RNG is a splitmix64 stream. It is not safe for concurrent use; each
// goroutine that needs randomness should own its own RNG (derive one with
// Fork so sub-streams stay reproducible).
type RNG struct {
	state uint64
}

// New returns a generator seeded with the given value. Seed 0 is fine —
// splitmix64 has no weak seeds.
func New(seed uint64) *RNG {
	return &RNG{state: seed}
}

// Next returns the next 64-bit value in the stream.
func (r *RNG) Next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Fork returns a new independent stream whose seed is derived from this
// one's next output. It is used to give each concern (durations, signature
// modes, shuffling) its own sub-stream, so adding a draw in one place does
// not shift the numbers every other place sees.
func (r *RNG) Fork() *RNG {
	return New(r.Next())
}

// Intn returns a uniformly distributed value in [0, n). It panics if n <= 0,
// matching the contract of math/rand.Intn. Rejection sampling removes the
// modulo bias that a plain `Next() % n` would introduce for n that do not
// divide 2^64.
func (r *RNG) Intn(n int) int {
	if n <= 0 {
		panic("rng: Intn requires n > 0")
	}
	un := uint64(n)
	// Largest multiple of un that fits in uint64; draws above it are rejected.
	limit := ^uint64(0) - (^uint64(0) % un)
	for {
		v := r.Next()
		if v < limit {
			return int(v % un)
		}
	}
}

// Float returns a value in [0, 1) with 53 bits of precision.
func (r *RNG) Float() float64 {
	return float64(r.Next()>>11) / (1 << 53)
}

// Chance reports whether an event with probability p occurs. p <= 0 is
// always false and p >= 1 is always true, so callers can pass 0 to disable a
// fault without a special case.
func (r *RNG) Chance(p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	return r.Float() < p
}

// Shuffle applies a Fisher–Yates permutation to n elements using swap. The
// permutation is a pure function of the generator's current state, so a
// seeded plan reorders deliveries identically everywhere.
func (r *RNG) Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		swap(i, j)
	}
}
