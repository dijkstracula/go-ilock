package ilock

import (
	"log"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const serialConcurrency = 1
const lowConcurrency = 2
const mediumConcurrency = 10
const highConcurrency = 20

const noWritePerc = 0
const writePerc = 1
const heavyWritePerc = 10

/* Ensure the values are nondecreasing.  If there is a non-decreasing
* value, because each writer should take some lock at some index and
* increment all subsequent indices too, if there's a decreasing value
* observed then we know we're not linearizing our write operations. */
func testNonDecreasing(b *testing.B, values []uint32) {
	for i := 1; i < len(values); i++ {
		assert.LessOrEqual(b, values[i-1], values[i], "Nondecreasing value")
	}
}

func BenchmarkSerialNoWrites(b *testing.B) {
	ret := benchmarkLocking(b, serialConcurrency, noWritePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkSerial(b *testing.B) {
	ret := benchmarkLocking(b, serialConcurrency, writePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkSerialHeavyLocking(b *testing.B) {
	ret := benchmarkLocking(b, serialConcurrency, heavyWritePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkLowConcurrency(b *testing.B) {
	ret := benchmarkLocking(b, lowConcurrency, writePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkMediumConcurrency(b *testing.B) {
	ret := benchmarkLocking(b, mediumConcurrency, writePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkHighConcurrencyNoWrites(b *testing.B) {
	ret := benchmarkLocking(b, highConcurrency, noWritePerc)
	testNonDecreasing(b, ret)
}

func BenchmarkHighConcurrency(b *testing.B) {
	benchmarkLocking(b, highConcurrency, writePerc)
}

func BenchmarkHighConcurrencyHeavyWrites(b *testing.B) {
	benchmarkLocking(b, highConcurrency, heavyWritePerc)
}

/* This test simulates `concurrency` actors acting on a "branch"
 * of a tree of data.  mutexes[i] is responsible explicitly for
 * values[i] and all subsequent values, implicitly.
 */
func benchmarkLocking(b *testing.B, concurrency int, writePerc int) []uint32 {
	l := log.New(os.Stderr, "", 0)
	//l.SetOutput(ioutil.Discard)
	barrier := make(chan bool, concurrency)

	/* mutexes[i] encapsulates values[i..9] */
	var mutexes [20]*Mutex
	var values [20]uint32

	for i := 0; i < len(mutexes); i++ {
		mutexes[i] = New()
	}

	sHandler := func(offset int) {
		for i := 0; i < offset; i++ {
			mutexes[i].ISLock()
			//	l.Printf("sHandler -> IS %d %d\n", i, offset)
		}
		mutexes[offset].SLock()
		l.Printf("sHandler -> S %d\n", offset)

		var garbage uint32
		for i := offset; i < len(values); i++ {
			garbage += values[i]
		}

		mutexes[offset].SUnlock()
		l.Printf("sHandler <- S %d\n", offset)

		for i := offset - 1; i >= 0; i-- {
			mutexes[i].ISUnlock()
			//l.Printf("sHandler <- IS %d %d\n", i, offset)
		}
		<-barrier
	}

	xHandler := func(offset int) {
		for i := 0; i < offset; i++ {
			mutexes[i].IXLock()
			l.Printf("xHandler -> IX %d %d\n", i, offset)
		}
		mutexes[offset].XLock()
		l.Printf("xHandler -> X %d\n", offset)

		for i := offset; i < len(values); i++ {
			values[i]++
		}

		mutexes[offset].XUnlock()
		l.Printf("xHandler <- X %d\n", offset)

		for i := offset - 1; i >= 0; i-- {
			mutexes[i].IXUnlock()
			l.Printf("xHandler <- IX %d %d\n", i, offset)
		}
		<-barrier
	}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		rw := rand.Intn(100) <= writePerc
		offset := rand.Intn(len(mutexes))

		barrier <- true
		if rw {
			go xHandler(offset)
		} else {
			go sHandler(offset)
		}
	}
	b.StopTimer()

	for {
		select {
		case <-barrier:
		default:
			// Looks like the race detector spuriously complains about a benign race
			// as we read the elements in values without being protected by a lock
			// in the caller. (I'm assuming that the Go race detector does an Eraser-style
			// race detection procedure where it looks for an empty intersection of
			// locks held on accesses).
			mutexes[0].XLock()
			ret := append([]uint32(nil), values[:]...)
			mutexes[0].XUnlock()
			return ret
		}
	}
}

func TestExtractIXIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders
		newState := setIX(state, val)

		assert.Equal(t, val, extractIX(newState), "expected %016x; got %016x", val, extractIX(newState))
		assert.Equal(t, extractIS(newState), extractIS(state), "expected %016x; got %016x", extractIS(state), extractIS(newState))
		assert.Equal(t, extractS(newState), extractS(state), "expected %016x; got %016x", extractIS(state), extractIS(newState))
		assert.Equal(t, extractX(newState), extractX(state), "expected %016x; got %016x", extractIS(state), extractIS(newState))
	}
}
func TestExtractISIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setIS(state, val)
		assert.Equal(t, extractIS(newState), val, "expected %016x; got %016x", val, extractIS(newState))
		assert.Equal(t, extractIX(newState), extractIX(state), "expected %016x; got %016x", extractIX(state), extractIX(newState))
		assert.Equal(t, extractS(newState), extractS(state), "expected %016x; got %016x", extractS(state), extractS(newState))
		assert.Equal(t, extractX(newState), extractX(state), "expected %016x; got %016x", extractX(state), extractX(newState))
	}
}
func TestExtractSIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setS(state, val)
		assert.Equal(t, extractS(newState), val, "expected %016x; got %016x", val, extractIS(newState))
		assert.Equal(t, extractIX(newState), extractIX(state), "expected %016x; got %016x", extractIX(state), extractIX(newState))
		assert.Equal(t, extractIS(newState), extractIS(state), "expected %016x; got %016x", extractS(state), extractS(newState))
		assert.Equal(t, extractX(newState), extractX(state), "expected %016x; got %016x", extractX(state), extractX(newState))
	}
}
func TestExtractXIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setX(state, val)
		assert.Equal(t, extractX(newState), val, "expected %016x; got %016x", val, extractIS(newState))
		assert.Equal(t, extractS(newState), extractS(state), "expected %016x; got %016x", extractX(state), extractX(newState))
		assert.Equal(t, extractIX(newState), extractIX(state), "expected %016x; got %016x", extractIX(state), extractIX(newState))
		assert.Equal(t, extractIS(newState), extractIS(state), "expected %016x; got %016x", extractS(state), extractS(newState))
	}
}

func TestRegisterX(t *testing.T) {
	var m *Mutex

	// X -> X
	m = New()
	assert.True(t, m.registerX(), "Failure to register X state from nascent Mutex")
	assert.False(t, m.registerX(), "Failure to ensure mutual writer exclusion")

	// X -> S
	m = New()
	assert.True(t, m.registerX(), "Failure to register X state from nascent Mutex")
	assert.False(t, m.registerS(), "Failure to ensure mutual writer exclusion")

	// X -> IS
	m = New()
	assert.True(t, m.registerX(), "Failure to register X state from nascent Mutex")
	assert.False(t, m.registerIS(), "Failure to ensure mutual writer exclusion")

	// X -> IX
	m = New()
	assert.True(t, m.registerX(), "Failure to register X state from nascent Mutex")
	assert.False(t, m.registerIX(), "Failure to ensure mutual writer exclusion")
}

func TestRegisterS(t *testing.T) {
	var m *Mutex

	// S -> X
	m = New()
	assert.True(t, m.registerS(), "Failure to register S state from nascent Mutex")
	assert.False(t, m.registerX(), "Failure to ensure mutual writer exclusion")

	// S -> S
	m = New()
	assert.True(t, m.registerS(), "Failure to register S state from nascent Mutex")
	assert.True(t, m.registerS(), "Failure to allow simultaneous S states")

	// S -> IS
	m = New()
	assert.True(t, m.registerS(), "Failure to register S state from nascent Mutex")
	assert.True(t, m.registerIS(), "Failure to allow simultaneous S and IS states")

	// S -> IX
	m = New()
	assert.True(t, m.registerS(), "Failure to register S state from nascent Mutex")
	assert.False(t, m.registerIX(), "Allows simultaneous S and IX states")
}

func TestRegisterIS(t *testing.T) {
	var m *Mutex

	// IS -> X
	m = New()
	assert.True(t, m.registerIS(), "Failure to register IS state from nascent Mutex")
	assert.False(t, m.registerX(), "Failure to ensure mutual writer exclusion")

	// IS -> S
	m = New()
	assert.True(t, m.registerIS(), "Failure to register IS state from nascent Mutex")
	assert.True(t, m.registerS(), "Failure to allow simultaneous S and IS states")

	// IS -> IS
	m = New()
	assert.True(t, m.registerIS(), "Failure to register IS state from nascent Mutex")
	assert.True(t, m.registerIS(), "Failure to allow simultaneous IS states")

	// IS -> IX
	m = New()
	assert.True(t, m.registerIS(), "Failure to register IS state from nascent Mutex")
	assert.True(t, m.registerIX(), "Failure to allow simultaneous IS and IX states")
}

func TestRegisterIX(t *testing.T) {
	var m *Mutex

	// IX -> X
	m = New()
	assert.True(t, m.registerIX(), "Failure to register IX state from nascent Mutex")
	assert.False(t, m.registerX(), "Failure to ensure mutual writer exclusion")

	// IX -> S
	m = New()
	assert.True(t, m.registerIX(), "Failure to register IX state from nascent Mutex")
	assert.False(t, m.registerS(), "Holding IX and S states simultaneously")

	// IX -> IS
	m = New()
	assert.True(t, m.registerIX(), "Failure to register IX state from nascent Mutex")
	assert.True(t, m.registerIS(), "Failure to allow simultaneous IS and IX states")

	// IX -> IX
	m = New()
	assert.True(t, m.registerIX(), "Failure to register IX state from nascent Mutex")
	assert.True(t, m.registerIX(), "Failure to allow simultaneous IX states")
}

type op int

const (
	Read  = 1
	Write = 2
)

// This test ensures that if we have multiple readers and writers contending on an
// intention lock, that we do not proceed with any of the writers until all the readers
// have been processed.
func TestDrainReads(t *testing.T) {
	l := log.New(os.Stderr, "", 0)
	l.SetOutput(os.Stderr)

	begin := time.Now()
	end := begin.Add(5 * time.Second)

	readers := 5
	writers := 5

	for time.Now().Before(end) {
		l.Printf("----")
		ch := make(chan op, readers+writers+1)

		// Grab the lock
		mtx := New()
		mtx.XLock()

		var wg sync.WaitGroup
		wg.Add(readers + writers)

		// Fire off a set number of readers and writers.
		for i := 0; i < readers; i++ {
			go func(i int) {
				l.Printf("reader %d slocking\n", i)
				wg.Done()
				mtx.SLock()
				l.Printf("reader %d slocked\n", i)
				ch <- Read
				mtx.SUnlock()
			}(i)
		}
		for i := 0; i < writers; i++ {
			go func(i int) {
				l.Printf("writer %d xlocking\n", i)
				wg.Done()
				mtx.XLock()
				l.Printf("writer %d xlocked\n", i)
				ch <- Write
				mtx.XUnlock()
			}(i)
		}

		// We can't use a WaitGroup or condvar to wait for the mutex being
		// correctly primed because there would be a tiny race if we signaled
		// before the lock and of course signaling after the lock is too late.
		// I hate this too, yes.
		wg.Wait()
		time.Sleep(5 * time.Millisecond)

		// Unleash the hounds!  All the writers should be allowed to proceed
		// before the readers.
		mtx.XUnlock()

		seenRead := false
		for i := 0; i < readers+writers; i++ {
			ret := <-ch
			if seenRead && ret == Write {
				//assert.True(t, !seenRead, "saw a write after we saw a read")
			}
			if ret == Read {
				seenRead = true
			}
		}

	}
}
