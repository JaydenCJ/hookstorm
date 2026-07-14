// Tests for the deterministic generator. The whole value of hookstorm's
// reproducibility rests on these: if the stream ever changes, a seed stops
// reproducing its storm, so a couple of golden values are pinned on purpose.
package rng

import "testing"

// The first outputs of splitmix64 for seed 0 are a well-known reference
// vector; pinning them guarantees we did not accidentally alter the algorithm.
func TestSeedZeroGoldenStream(t *testing.T) {
	want := []uint64{16294208416658607535, 7960286522194355700, 487617019471545679}
	r := New(0)
	for i, w := range want {
		if got := r.Next(); got != w {
			t.Fatalf("Next() #%d = %d, want %d", i, got, w)
		}
	}
}

func TestSameSeedSameSequence(t *testing.T) {
	a, b := New(12345), New(12345)
	for i := 0; i < 1000; i++ {
		if x, y := a.Next(), b.Next(); x != y {
			t.Fatalf("streams diverged at %d: %d != %d", i, x, y)
		}
	}
}

func TestForkIsDeterministicButIndependent(t *testing.T) {
	// Fork must be reproducible...
	f1 := New(99).Fork()
	f2 := New(99).Fork()
	for i := 0; i < 100; i++ {
		if f1.Next() != f2.Next() {
			t.Fatalf("forked streams from the same seed diverged at %d", i)
		}
	}
	// ...and a fork must not simply echo its parent's stream.
	parent := New(7)
	child := parent.Fork()
	if parent.Next() == child.Next() {
		t.Fatal("child stream echoes the parent")
	}
}

func TestIntnInRange(t *testing.T) {
	r := New(4)
	for _, n := range []int{1, 2, 3, 7, 10, 100, 1000} {
		for i := 0; i < 500; i++ {
			v := r.Intn(n)
			if v < 0 || v >= n {
				t.Fatalf("Intn(%d) = %d out of range", n, v)
			}
		}
	}
}

func TestIntnOneAlwaysZero(t *testing.T) {
	r := New(4)
	for i := 0; i < 100; i++ {
		if v := r.Intn(1); v != 0 {
			t.Fatalf("Intn(1) = %d, want 0", v)
		}
	}
}

func TestIntnPanicsOnNonPositive(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Intn(0) should panic")
		}
	}()
	New(1).Intn(0)
}

func TestFloatInUnitInterval(t *testing.T) {
	r := New(8)
	for i := 0; i < 5000; i++ {
		f := r.Float()
		if f < 0 || f >= 1 {
			t.Fatalf("Float() = %v, want [0,1)", f)
		}
	}
}

func TestChanceBoundaries(t *testing.T) {
	r := New(1)
	for i := 0; i < 100; i++ {
		if r.Chance(0) {
			t.Fatal("Chance(0) returned true")
		}
		if !r.Chance(1) {
			t.Fatal("Chance(1) returned false")
		}
	}
}

func TestShuffleIsPermutation(t *testing.T) {
	r := New(3)
	xs := make([]int, 50)
	for i := range xs {
		xs[i] = i
	}
	r.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
	seen := make(map[int]bool, len(xs))
	for _, x := range xs {
		if x < 0 || x >= len(xs) || seen[x] {
			t.Fatalf("shuffle is not a permutation: bad element %d", x)
		}
		seen[x] = true
	}
}

func TestShuffleDeterministic(t *testing.T) {
	build := func() []int {
		xs := make([]int, 20)
		for i := range xs {
			xs[i] = i
		}
		r := New(555)
		r.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
		return xs
	}
	a, b := build(), build()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("shuffle diverged at %d: %d != %d", i, a[i], b[i])
		}
	}
}
