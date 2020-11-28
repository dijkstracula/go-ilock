# go-ilock

A simple intention lock implementation for Go.

## Background

Consider a concurrent tree-like data structure.  A classic example that relate
to intention locks is a database index structure; in the example below, we are
interested in something more approching a trie, where intermediary nodes
represent a prefix of some larger string.  A larger system would like to have
concurrent read and write access on strings in this trie, including the option
of locking not just the leaves of the trie (ie. an entire string) but intermediary
nodes in the trie (that is, all strings with a common prefix).

Nodes may be locked by a reader (allowing concurrent reads) or a writer
(mandating exclusive accsess)  If a node is locked by a particular mutator
thread, the following invariants have to be maintained:

1)  The owning thread may access any part of the node's subtree (ie. its
    enclosing fields and/or array elements) without taking further locks in or
    otherwise traversing the subtree itself.

2)  Other threads should not be blocked from executing independent operations on
    different parts of the prefix tree.

It would be trivial to implement 1) but not 2) by way of a global reader-writer
lock on the entire tree.  The obvious extension of putting finer-grained
reader-writer locks on each node is not sufficient, because we still need to
ensure that taking the lock on a particular node does not violate 1) in the
case where a descendent node is locked by a different thread.  In order to
ensure this "hierarchial mutual exclusion", we would have to traverse the
node's subtree (or equivalently, walk a table of held locks), which violates
1); or, we would have to lock each parent node as we descend, which violates
2).  Indeed, in our case, since our "graph of nodes" is a tree and thus has a
single root, this would be equivalent to serializing with the global RW lock in
the first place!

We will address this by way of taking _intention locks_ as we traverse the prefix
tree up until the current node.  This library implements such a lock.

## Overview

An intention lock is similar to a reader-writer lock in that it has multiple
states that can be set, which may or may not cause the caller to block.  These
two states, indicating a read-only shared and read-write exclusive state
are called, in the traditional literature, states `S` and `X`.  A node set in
`S` or `X` _implicitly_ has all its descendent nodes set similarily; as a
result, this won't work for arbitrary DAG or graph structures; we need exactly
one path to a given node.

There also exist provisional intention states for taking the lock as a reader
and as as writer, called, respectively, IS and IX.

`IS` is "Intention to Share": that is, it grants permission for the requesting
thread to continue traversing the tree and set subsquent elements to IS or
S.  The only way that a node taking IS can fail is if the current node is
held in the X state: that is, that another thread owns that thread for write
access.

`IX` is "Intention for eXclusive access": that is, it grants permission for the
requesting thread to continue traversing the tree and set subsequent elements
to `IX` and `X` states, as well as any state that can be set had `IS` been set
(though I think this is a characteristic that we don't need.)  Note that IX's
semantics relate differently to IS as compared to `X` as compared to `S`; a
node can be held by one thread in `IS` and also held simultaneously in `IX`.

`SIX` is "Intention to Share and upgrade to IX`: our particular usecase does
not require this state and so it is left unimplemented.

Therefore, taking a shared lock on some node requires setting all ancestors to
`IS` (blocking if necessary) and setting the node itself to `S`, and taking an
exclusive lock on some node requires setting all ancestors to `IX` (blocking if
necessary) and setting the node itself to `X`.

The transition matrix for all states is presented below.  If a transition is
not allowed, the caller will block.

```
+---------------+----------+-----------+-----------+------------+------------+
|Request/Holding| Unlocked | Holding X | Holding S | Holding IX | Holding IS |
+---------------+----------+-----------+-----------+------------+------------+
|Request X      |   Yes    |    No     |    No     |     No     |     No     |
|Request S      |   Yes    |    No     |    Yes    |     No     |     Yes    |
|Request IX     |   Yes    |    No     |    No     |     Yes    |     Yes    |
|Request IS     |   Yes    |    No     |    Yes    |     Yes    |     Yes    |
+---------------+----------+-----------+-----------+------------+------------+
```
