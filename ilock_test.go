package ilock

import (
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var workloads = []struct {
	name        string
	concurrency int
	writeRatio  float32
}{
	{"Serial", 1, 0.10},
	{"Serial, heavy writes", 1, 0.50},
	{"Low concurrency", 2, 0.10},
	{"Medium concurrency", 10, 0.10},
	{"High concurrency", 20, 0.10},
	{"High concurrency, heavy writes", 20, 0.50},
}

const serialConcurrency = 1
const lowConcurrency = 2
const mediumConcurrency = 10
const highConcurrency = 20

const writeFrac = 0.1
const heavyWriteFrac = 0.5

/* Ensure the values are nondecreasing.  If there is a non-decreasing
* value, because each writer should take some lock at some index and
* increment all subsequent indices too, if there's a decreasing value
* observed then we know we're not linearizing our write operations. */
func testNonDecreasing(b *testing.B, values []uint32) {
	for i := 1; i < len(values); i++ {
		assert.LessOrEqual(b, values[i-1], values[i], "Nondecreasing value")
	}
}

func BenchmarkSerial(b *testing.B) {
	ret := benchmarkLocking(b, serialConcurrency, int(writeFrac*100))
	testNonDecreasing(b, ret)
}

func BenchmarkSerialHeavyLocking(b *testing.B) {
	ret := benchmarkLocking(b, serialConcurrency, int(heavyWriteFrac*100))
	testNonDecreasing(b, ret)
}

func BenchmarkLowConcurrency(b *testing.B) {
	ret := benchmarkLocking(b, lowConcurrency, int(writeFrac*100))
	testNonDecreasing(b, ret)
}

func BenchmarkMediumConcurrency(b *testing.B) {
	ret := benchmarkLocking(b, mediumConcurrency, int(writeFrac*100))
	testNonDecreasing(b, ret)
}
func BenchmarkHighConcurrency(b *testing.B) {
	benchmarkLocking(b, highConcurrency, int(writeFrac*100))
}

func BenchmarkHighConcurrencyHeavyLocking(b *testing.B) {
	benchmarkLocking(b, highConcurrency, int(heavyWriteFrac*100))
}

/* This test simulates `concurrency` actors acting on a "branch"
 * of a tree of data.  mutexes[i] is responsible explicitly for
 * values[i] and all subsequent values, implicitly.
 */
func benchmarkLocking(b *testing.B, concurrency int, writePerc int) []uint32 {
	l := log.New(os.Stderr, "", 0)
	l.SetOutput(ioutil.Discard)
	barrier := make(chan bool, concurrency)

	/* mutexes[i] encapsulates values[i..9] */
	var mutexes [10]*Mutex
	var values [10]uint32

	for i := 0; i < len(mutexes); i++ {
		mutexes[i] = New()
	}

	ixHandler := func(offset int) {
		for i := 0; i <= offset; i++ {
			mutexes[i].IXLock()
			l.Printf("ixHandler -> %d %d\n", i, offset)
		}
		for i := offset; i >= 0; i-- {
			mutexes[i].IXUnlock()
			l.Printf("ixHandler <- %d %d\n", i, offset)
		}
		<-barrier
	}

	isHandler := func(offset int) {
		for i := 0; i <= offset; i++ {
			mutexes[i].ISLock()
			l.Printf("isHandler -> %d %d\n", i, offset)
		}
		for i := offset; i >= 0; i-- {
			mutexes[i].ISUnlock()
			l.Printf("isHandler <- %d %d\n", i, offset)
		}
		<-barrier
	}

	sHandler := func(offset int) {
		for i := 0; i < offset; i++ {
			mutexes[i].ISLock()
			l.Printf("sHandler -> IS %d %d\n", i, offset)
		}
		mutexes[offset].SLock()
		l.Printf("sHandler -> S %d\n", offset)

		mutexes[offset].SUnlock()
		l.Printf("sHandler <- S %d\n", offset)

		for i := offset - 1; i >= 0; i-- {
			mutexes[i].ISUnlock()
			l.Printf("sHandler <- IS %d %d\n", i, offset)
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

	for i := 0; i < b.N; i++ {
		rw := rand.Intn(100) < writePerc
		offset := rand.Intn(len(mutexes))

		barrier <- true
		if rw {
			go xHandler(offset)
		} else {
			switch rand.Intn(3) {
			case 0:
				go ixHandler(offset)
				break
			case 1:
				go isHandler(offset)
				break
			case 2:
				go sHandler(offset)
				break
			}
		}
	}

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
