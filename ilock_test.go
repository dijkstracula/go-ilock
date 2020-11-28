package ilock

import (
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"
)

func TestHighConcurrentWrites(t *testing.T) {
	testLocking(t, 20, 10)
}

func TestHighConcurrency(t *testing.T) {
	testLocking(t, 20, 1)
}
func TestMedConcurrency(t *testing.T) {
	testLocking(t, 5, 1)
}

func TestLowConcurrency(t *testing.T) {
	testLocking(t, 2, 1)
}

func TestSerialHeavyWrites(t *testing.T) {
	testLocking(t, 1, 20)
}
func TestSerial(t *testing.T) {
	testLocking(t, 1, 1)
}

/* This test simulates `concurrency` actors acting on a "branch"
 * of a tree of data.  mutexes[i] is responsible explicitly for
 * values[i] and all subsequent values, implicitly.
 */
func testLocking(t *testing.T, concurrency int, writePerc int) {
	l := log.New(os.Stderr, "", 0)
	l.SetOutput(ioutil.Discard)
	barrier := make(chan bool, concurrency)
	begin := time.Now()
	end := begin.Add(5 * time.Second)

	/* mutexes[i] encapsulates values[i..9] */
	var mutexes [10]*Mutex
	var values [10]uint32

	for i := 0; i < len(mutexes); i++ {
		mutexes[i] = New()
	}

	ixHandler := func(offset int, delay time.Duration) {
		for i := 0; i <= offset; i++ {
			mutexes[i].IXLock()
			l.Printf("ixHandler -> %d %d\n", i, offset)
		}
		time.Sleep(delay)
		for i := offset; i >= 0; i-- {
			mutexes[i].IXUnlock()
			l.Printf("ixHandler <- %d %d\n", i, offset)
		}
		<-barrier
	}

	isHandler := func(offset int, delay time.Duration) {
		for i := 0; i <= offset; i++ {
			mutexes[i].ISLock()
			l.Printf("isHandler -> %d %d\n", i, offset)
		}
		time.Sleep(delay)
		for i := offset; i >= 0; i-- {
			mutexes[i].ISUnlock()
			l.Printf("isHandler <- %d %d\n", i, offset)
		}
		<-barrier
	}

	sHandler := func(offset int, delay time.Duration) {
		var garbage uint32
		for i := 0; i < offset; i++ {
			mutexes[i].ISLock()
			l.Printf("sHandler -> IS %d %d\n", i, offset)
		}
		mutexes[offset].SLock()
		l.Printf("sHandler -> S %d\n", offset)

		for i := offset; i < len(values); i++ {
			garbage += values[i]
		}

		time.Sleep(delay)
		mutexes[offset].SUnlock()
		l.Printf("sHandler <- S %d\n", offset)

		for i := offset - 1; i >= 0; i-- {
			mutexes[i].ISUnlock()
			l.Printf("sHandler <- IS %d %d\n", i, offset)
		}
		<-barrier
	}

	xHandler := func(offset int, delay time.Duration) {
		for i := 0; i < offset; i++ {
			mutexes[i].IXLock()
			l.Printf("xHandler -> IX %d %d\n", i, offset)
		}
		mutexes[offset].XLock()
		l.Printf("xHandler -> X %d\n", offset)

		for i := offset; i < len(values); i++ {
			values[i]++
		}
		time.Sleep(delay)

		mutexes[offset].XUnlock()
		l.Printf("xHandler <- X %d\n", offset)

		for i := offset - 1; i >= 0; i-- {
			mutexes[i].IXUnlock()
			l.Printf("xHandler <- IX %d %d\n", i, offset)
		}
		<-barrier
	}

	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for time.Now().Before(end) {
		rw := rng.Intn(100) < writePerc
		delay := time.Duration(100+rng.Intn(5000)) * time.Microsecond
		i := rng.Intn(len(mutexes))

		barrier <- true
		if rw {
			go xHandler(i, delay)
		} else {
			switch rng.Intn(3) {
			case 0:
				go ixHandler(i, delay)
				break
			case 1:
				go isHandler(i, delay)
				break
			case 2:
				go sHandler(i, delay)
				break
			}
		}
	}

	for {
		select {
		case <-barrier:
		default:
			/* Ensure the values are nondecreasing. */
			v := values[0]
			t.Logf("Final values: %v\n", values)
			for i := 1; i < len(values); i++ {
				if values[i] < v {
					t.Errorf("Nondecreasing value at index %d\n", i)
					v = values[i]
					break
				}
			}
			return
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
		if extractIX(newState) != val {
			t.Errorf("expected %016x; got %016x", val, extractIX(newState))
		}
		if extractIS(newState) != extractIS(state) {
			t.Errorf("expected %016x; got %016x", extractIS(state), extractIS(newState))
		}
		if extractS(newState) != extractS(state) {
			t.Errorf("expected %016x; got %016x", extractIS(state), extractIS(newState))
		}
		if extractX(newState) != extractX(state) {
			t.Errorf("expected %016x; got %016x", extractIS(state), extractIS(newState))
		}
	}
}
func TestExtractISIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setIS(state, val)
		if extractIS(newState) != val {
			t.Errorf("expected %016x; got %016x", val, extractIS(newState))
		}
		if extractIX(newState) != extractIX(state) {
			t.Errorf("expected %016x; got %016x", extractIX(state), extractIX(newState))
		}
		if extractS(newState) != extractS(state) {
			t.Errorf("expected %016x; got %016x", extractS(state), extractS(newState))
		}
		if extractX(newState) != extractX(state) {
			t.Errorf("expected %016x; got %016x", extractX(state), extractX(newState))
		}
	}
}
func TestExtractSIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setS(state, val)
		if extractS(newState) != val {
			t.Errorf("expected %016x; got %016x", val, extractS(newState))
		}
		if extractIX(newState) != extractIX(state) {
			t.Errorf("expected %016x; got %016x", extractIX(state), extractIX(newState))
		}
		if extractIS(newState) != extractIS(state) {
			t.Errorf("expected %016x; got %016x", extractIS(state), extractIS(newState))
		}
		if extractX(newState) != extractX(state) {
			t.Errorf("expected %016x; got %016x", extractX(state), extractX(newState))
		}
	}
}
func TestExtractXIdempotency(t *testing.T) {
	seed := time.Now().UTC().UnixNano()
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 100; i++ {
		state := rng.Uint64()
		val := rng.Uint64() & maxHolders

		newState := setX(state, val)
		if extractX(newState) != val {
			t.Errorf("expected %016x; got %016x", val, extractX(newState))
		}
		if extractIX(newState) != extractIX(state) {
			t.Errorf("expected %016x; got %016x", extractS(state), extractS(newState))
		}
		if extractIS(newState) != extractIS(state) {
			t.Errorf("expected %016x; got %016x", extractIS(state), extractIS(newState))
		}
		if extractS(newState) != extractS(state) {
			t.Errorf("expected %016x; got %016x", extractS(state), extractS(newState))
		}
	}
}

func TestRegisterX(t *testing.T) {
	var m *Mutex

	// X -> X
	m = New()
	if !m.registerX() {
		t.Errorf("Failure to register X state from nascent Mutex")
	}
	if m.registerX() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// X -> S
	m = New()
	if !m.registerX() {
		t.Errorf("Failure to register X state from nascent Mutex")
	}
	if m.registerS() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// X -> IS
	m = New()
	if !m.registerX() {
		t.Errorf("Failure to register X state from nascent Mutex")
	}
	if m.registerIS() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// X -> IX
	m = New()
	if !m.registerX() {
		t.Errorf("Failure to register X state from nascent Mutex")
	}
	if m.registerIX() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}
}

func TestRegisterS(t *testing.T) {
	var m *Mutex

	// S -> X
	m = New()
	if !m.registerS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if m.registerX() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// S -> S
	m = New()
	if !m.registerS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if !m.registerS() {
		t.Errorf("Failure to allow simultaneously S states")
	}

	// S -> IS
	m = New()
	if !m.registerS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if !m.registerIS() {
		t.Errorf("Failure to allow simultaneously S and IS states")
	}

	// S -> IX
	m = New()
	if !m.registerS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if m.registerIX() {
		t.Errorf("Allows simultaneously S and IX states")
	}
}

func TestRegisterIS(t *testing.T) {
	var m *Mutex

	// IS -> X
	m = New()
	if !m.registerIS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if m.registerX() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// IS -> S
	m = New()
	if !m.registerIS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if !m.registerS() {
		t.Errorf("Failure to allow simultaneous S and IS states")
	}

	// IS -> IS
	m = New()
	if !m.registerIS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if !m.registerIS() {
		t.Errorf("Failure to allow simultaneous IS states")
	}

	// IS -> IX
	m = New()
	if !m.registerIS() {
		t.Errorf("Failure to register S state from nascent Mutex")
	}
	if !m.registerIX() {
		t.Errorf("Failure to allow simultaneous IS and IX states")
	}
}

func TestRegisterIX(t *testing.T) {
	var m *Mutex

	// IX -> X
	m = New()
	if !m.registerIX() {
		t.Errorf("Failure to register IX state from nascent Mutex")
	}
	if m.registerX() {
		t.Errorf("Failure to ensure mutual exclusion on the X state")
	}

	// IX -> S
	m = New()
	if !m.registerIX() {
		t.Errorf("Failure to register IX state from nascent Mutex")
	}
	if m.registerS() {
		t.Errorf("Holding IX and S states simultaneously")
	}

	// IX -> IS
	m = New()
	if !m.registerIX() {
		t.Errorf("Failure to register IX state from nascent Mutex")
	}
	if !m.registerIS() {
		t.Errorf("Failure to allow simultaneous IX and IS states")
	}

	// IX -> IX
	m = New()
	if !m.registerIX() {
		t.Errorf("Failure to register IX state from nascent Mutex")
	}
	if !m.registerIX() {
		t.Errorf("Failure to allow simultaneous IX states")
	}
}
