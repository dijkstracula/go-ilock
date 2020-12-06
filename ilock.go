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

// Package ilock implements the following lock structure, called an "intention
// lock":
//
// Consider a concurrent tree-like data structure.  A classic example that relate
// to intention locks is a database index structure; in the example below, we are
// interested in something more approching a trie, where intermediary nodes
// represent a prefix of some larger string.  A larger system would like to have
// concurrent read and write access on strings in this trie, including the option
// of locking not just the leaves of the trie (ie. an entire string) but intermediary
// nodes in the trie (that is, all strings with a common prefix).
//
// Nodes may be locked by a reader (allowing concurrent reads) or a writer
// (mandating exclusive accsess)  If a node is locked by a particular mutator
// thread, the following invariants have to be maintained:
//
// 1)  The owning thread may access any part of the node's subtree (ie. its
// enclosing fields and/or array elements) without taking further locks in or
// otherwise traversing the subtree itself.
//
// 2)  Other threads should not be blocked from executing independent operations on
// different parts of the prefix tree.
//
// It would be trivial to implement 1) but not 2) by way of a global reader-writer
// lock on the entire tree.  The obvious extension of putting finer-grained
// reader-writer locks on each node is not sufficient, because we still need to
// ensure that taking the lock on a particular node does not violate 1) in the
// case where a descendent node is locked by a different thread.  In order to
// ensure this "hierarchial mutual exclusion", we would have to traverse the
// node's subtree (or equivalently, walk a table of held locks), which violates
// 1); or, we would have to lock each parent node as we descend, which violates
// 2).  Indeed, in our case, since our "graph of nodes" is a tree and thus has a
// single root, this would be equivalent to serializing with the global RW lock in
// the first place!
//
// We will address this by way of taking _intention locks_ as we traverse the prefix
// tree up until the current node.  This library implements such a lock.
//
// ## Overview
//
// An intention lock is similar to a reader-writer lock in that it has multiple
// states that can be set, which may or may not cause the caller to block.  These
// two states, indicating a read-only shared and read-write exclusive state
// are called, in the traditional literature, states `S` and `X`.  A node set in
// `S` or `X` _implicitly_ has all its descendent nodes set similarily; as a
// result, this won't work for arbitrary DAG or graph structures; we need exactly
// one path to a given node.
//
// There also exist provisional intention states for taking the lock as a reader
// and as as writer, called, respectively, IS and IX.
//
// `IS` is "Intention to Share": that is, it grants permission for the requesting
// thread to continue traversing the tree and set subsquent elements to IS or
// S.  The only way that a node taking IS can fail is if the current node is
// held in the X state: that is, that another thread owns that thread for write
// access.
//
// `IX` is "Intention for eXclusive access": that is, it grants permission for the
// requesting thread to continue traversing the tree and set subsequent elements
// to `IX` and `X` states, as well as any state that can be set had `IS` been set
// (though I think this is a characteristic that we don't need.)  Note that IX's
// semantics relate differently to IS as compared to `X` as compared to `S`; a
// node can be held by one thread in `IS` and also held simultaneously in `IX`.
//
// `SIX` is "Intention to Share and upgrade to IX`: our particular usecase does
// not require this state and so it is left unimplemented.
//
// Therefore, taking a shared lock on some node requires setting all ancestors to
// `IS` (blocking if necessary) and setting the node itself to `S`, and taking an
// exclusive lock on some node requires setting all ancestors to `IX` (blocking if
// necessary) and setting the node itself to `X`.
//
// The transition matrix for all states is presented below.  If a transition is
// not allowed, the caller will block.
//
//     +---------------+----------+-----------+-----------+------------+------------+
//     |Request/Holding| Unlocked | Holding X | Holding S | Holding IX | Holding IS |
//     +---------------+----------+-----------+-----------+------------+------------+
//     |Request X      |   Yes    |    No     |    No     |     No     |     No     |
//     |Request S      |   Yes    |    No     |    Yes    |     No     |     Yes    |
//     |Request IX     |   Yes    |    No     |    No     |     Yes    |     Yes    |
//     |Request IS     |   Yes    |    No     |    Yes    |     Yes    |     Yes    |
//     +---------------+----------+-----------+-----------+------------+------------+
//
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
//     |63      48|47      32|31     16|15      0|
//      \   IX   / \   IS   / \   S   / \   X   /
//
//
type Mutex struct {
	mtx   sync.Mutex
	c     *sync.Cond // The condvar that mutator threads will wait on
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
	m.c = sync.NewCond(&m.mtx)
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
	m.mtx.Lock()
	for !compatableWithIS(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerIS()
	m.mtx.Unlock()
}

// ISUnlock removes the single writer's IS state value and schedule all
// blocked goroutines to run.
func (m *Mutex) ISUnlock() {
	var val uint64
	// Atomically unset our IS state.
	for {
		state := atomic.LoadUint64(&m.state)
		val = extractIS(state) - 1
		newState := setIS(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	// If the number of holders of this context has gone to zero, we should
	// see if anyone else can take the lock.
	if val == 0 {
		m.c.Broadcast()
	}
}

// IXLock takes the Mutex for shared read access. Blocks if the lock is
// currently held in any of the following states:
// X, S
func (m *Mutex) IXLock() {
	// Are the current states held compatable with this state?
	m.mtx.Lock()
	for !compatableWithIX(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerIX()
	m.mtx.Unlock()
}

// IXUnlock removes the single writer's IX state value and schedule all
// blocked goroutines to run.
func (m *Mutex) IXUnlock() {
	// Atomically unset our IX state.
	var val uint64
	for {
		state := atomic.LoadUint64(&m.state)
		val = extractIX(state) - 1
		newState := setIX(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	// If the number of holders of this context has gone to zero, we should
	// see if anyone else can take the lock.
	if val == 0 {
		m.c.Broadcast()
	}
}

// SLock takes the Mutex for shared read access. Blocks if the lock is
// currently held in any of the following states:
// X, IX
func (m *Mutex) SLock() {
	// Are the current states held compatable with this state?
	m.mtx.Lock()
	for !compatableWithS(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: SLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: SLock woke up!\n")
	}
	m.registerS()
	m.mtx.Unlock()
}

// SUnlock decrements the lock's S state value and schedules all
// blocked goroutines to run.
func (m *Mutex) SUnlock() {
	var val uint64
	// Atomically unset our S state.
	for {
		state := atomic.LoadUint64(&m.state)
		val = extractS(state) - 1
		newState := setS(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	// If the number of holders of this context has gone to zero, we should
	// see if anyone else can take the lock.
	if val == 0 {
		m.c.Broadcast()
	}
}

// XLock takes the Mutex for exclusive write access. Blocks if the lock is
// currently held in any of the following states:
// X, S, IS, IX
func (m *Mutex) XLock() {
	// Are the current states held compatable with this state?
	m.mtx.Lock()
	for !compatableWithX(atomic.LoadUint64(&m.state)) {
		//fmt.Printf("NBT: ISLock has to wait!\n")
		m.c.Wait() // No! Wait;
		//fmt.Printf("NBT: ISLock woke up!\n")
	}
	m.registerX()
	m.mtx.Unlock()
}

// XUnlock removes the single writer's X state value and schedule all
// blocked goroutines to run.
func (m *Mutex) XUnlock() {
	var val uint64
	// Atomically unset our X state.
	for {
		state := atomic.LoadUint64(&m.state)
		val = extractX(state) - 1
		newState := setX(state, val)
		if atomic.CompareAndSwapUint64(&m.state, state, newState) {
			break
		}
	}
	// If the number of holders of this context has gone to zero, we should
	// see if anyone else can take the lock.
	if val == 0 {
		m.c.Broadcast()
	}
}
