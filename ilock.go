// Copyright 2020 Nathan Taylor (nbtaylor@gmail.com)
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
// of the Software, and to permit persons to whom the Software is furnished to do
// so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package ilock implements the intention lock described in the README.
package ilock

import (
	"sync"
	"sync/atomic"
	"time"
)

// Mutex implements an intention lock.  User threads will attempt to
// take the lock in one of four "state contexts", as described in the readme,
// and may end up blocking if their desired state is incompatible with the
// states already held in the lock.
//
// There are really only two interesting attributes to an ilock.Mutex: A
// condvar acts as a barrier for threads wishing to transition to a state
// incompatible with the current state context set, and then the state
// representing how many threads have entered the lock as each of its
// states. In order to check the Mutex state in a lock-free manner, the four
// fields are packed into a single uint64:
//
// |63      48|47      32|31     16|15      0|
//  \   IX   / \   IS   / \   S   / \   X   /
//
// This implies a maximum of (2^21 - 1) state in the non-X state; of course, we
// need mutual exclusion on taking X, so only one bit is held there.
//
type Mutex struct {
	c     *sync.Cond
	state uint64
}

const xOffset uint64 = 0
const xMask uint64 = (1 << 16) - 1

const sOffset uint64 = 16
const sMask uint64 = ((1 << 32) - 1) & ^((1 << 16) - 1)

const isOffset uint64 = 32
const isMask uint64 = ((1 << 48) - 1) & ^((1 << 32) - 1)

const ixOffset uint64 = 48
const ixMask uint64 = 0xffffffffffffffff & ^((1 << 48) - 1)

const maxHolders = (1 << 16) - 1

const startingBackoff = 50 * time.Microsecond
const maxBackoff = 500 * time.Millisecond
const backoffFactor = 2

func extractX(state uint64) uint64 {
	return (state & xMask) >> xOffset
}

func setX(state, val uint64) uint64 {
	return (state & ^xMask) | (val << xOffset)
}

func compatableWithX(state uint64) bool {
	// e.g. no holders of any state context
	return (state == 0)
}

func extractS(state uint64) uint64 {
	return (state & sMask) >> sOffset
}

func setS(state, val uint64) uint64 {
	return (state & ^sMask) | (val << sOffset)
}

func compatableWithS(state uint64) bool {
	return extractX(state) == 0 && extractIX(state) == 0
}

func extractIX(state uint64) uint64 {
	return (state & ixMask) >> ixOffset
}

func setIX(state, val uint64) uint64 {
	return (state & ^ixMask) | (val << ixOffset)
}

func compatableWithIX(state uint64) bool {
	return extractX(state) == 0 && extractS(state) == 0
}

func extractIS(state uint64) uint64 {
	return (state & isMask) >> isOffset
}

func setIS(state, val uint64) uint64 {
	return (state & ^isMask) | (val << isOffset)
}

func compatableWithIS(state uint64) bool {
	return extractX(state) == 0
}

// New returns a new Mutex.
func New() *Mutex {
	var m Mutex
	var mtx sync.Mutex
	m.c = sync.NewCond(&mtx)
	return &m
}

// Registers the calling thread as a holder in the IS state.
// Returns whether this operation is compatible with the
// previous lock state.
func (m *Mutex) registerIS() bool {
	for {
		state := atomic.LoadUint64(&m.state)
		newState := setIS(state, extractIS(state)+1)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			// We registered ourselves in the state word; are we
			// compatable with the previous lock state?
			return compatableWithIS(state)
		}
	}
}

// Registers the calling thread as a holder in the IX state.
// Returns whether this operation is compatible with the
// previous lock state.
func (m *Mutex) registerIX() bool {
	for {
		state := atomic.LoadUint64(&m.state)
		newState := setIX(state, extractIX(state)+1)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			// We registered ourselves in the state word; are we
			// compatable with the previous lock state?
			return compatableWithIX(state)
		}
	}
}

// Registers the calling thread as a holder in the S state.
// Returns whether this operation is compatible with the
// previous lock state.
func (m *Mutex) registerS() bool {
	for {
		state := atomic.LoadUint64(&m.state)
		newState := setS(state, extractS(state)+1)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			// We registered ourselves in the state word; are we
			// compatable with the previous lock state?
			return compatableWithS(state)
		}
	}
}

// Registers the calling thread as a holder in the X state.
// Returns whether this operation is compatible with the
// previous lock state.
func (m *Mutex) registerX() bool {
	for {
		state := atomic.LoadUint64(&m.state)
		newState := setX(state, extractX(state)+1)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			// We registered ourselves in the state word; are we
			// compatable with the previous lock state?
			return compatableWithX(state)
		}
	}
}

// ISLock takes the Mutex for shared read access. Blocks if the lock is
// currently held in any of the following states:
// X, IX
func (m *Mutex) ISLock() {
	// Are the current states held compatable with this state?
	m.c.L.Lock()
	for !compatableWithIS(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerIS()
	m.c.L.Unlock()
}

// ISUnlock removes the single writer's IS state value and schedule all
// blocked goroutines to run.
func (m *Mutex) ISUnlock() {
	// Atomically unset our IS state.
	for {
		state := atomic.LoadUint64(&m.state)
		val := extractIS(state) - 1
		newState := setIS(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	m.c.Broadcast() // Wake everybody else up.
}

// IXLock takes the Mutex for shared read access. Blocks if the lock is
// currently held in any of the following states:
// X, S
func (m *Mutex) IXLock() {
	// Are the current states held compatable with this state?
	m.c.L.Lock()
	for !compatableWithIX(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerIX()
	m.c.L.Unlock()
}

// IXUnlock removes the single writer's IX state value and schedule all
// blocked goroutines to run.
func (m *Mutex) IXUnlock() {
	// Atomically unset our IX state.
	for {
		state := atomic.LoadUint64(&m.state)
		val := extractIX(state) - 1
		newState := setIX(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	m.c.Broadcast() // Wake everybody else up.
}

// SLock takes the Mutex for shared read access. Blocks if the lock is
// currently held in any of the following states:
// X, IX
func (m *Mutex) SLock() {
	// Are the current states held compatable with this state?
	m.c.L.Lock()
	for !compatableWithS(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: SLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: SLock woke up!\n")
	}
	m.registerS()
	m.c.L.Unlock()
}

// SUnlock decrements the lock's S state value and schedules all
// blocked goroutines to run.
func (m *Mutex) SUnlock() {
	// Atomically unset our S state.
	for {
		state := atomic.LoadUint64(&m.state)
		val := extractS(state) - 1
		newState := setS(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	m.c.Broadcast() // Wake everybody else up.
}

// XLock takes the Mutex for exclusive write access. Blocks if the lock is
// currently held in any of the following states:
// X, S, IS, IX
func (m *Mutex) XLock() {
	// Are the current states held compatable with this state?
	m.c.L.Lock()
	for !compatableWithX(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerX()
	m.c.L.Unlock()
}

// XUnlock removes the single writer's X state value and schedule all
// blocked goroutines to run.
func (m *Mutex) XUnlock() {
	// Atomically unset our X state.
	for {
		state := atomic.LoadUint64(&m.state)
		val := extractX(state) - 1
		newState := setX(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	m.c.Broadcast() // Wake everybody else up.
}
