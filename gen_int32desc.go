// Code generated by gen.go; DO NOT EDIT.

package skipset

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Int32SetDesc represents a set based on skip list.
type Int32SetDesc struct {
	length       int64
	highestLevel uint64 // highest level for now
	header       *int32nodeDesc
}

type int32nodeDesc struct {
	flags bitflag
	value int32
	next  optionalArray // [level]*int32nodeDesc
	mu    sync.Mutex
	level uint32
}

func newInt32NodeDesc(value int32, level int) *int32nodeDesc {
	n := &int32nodeDesc{
		value: value,
		level: uint32(level),
	}
	if level > op1 {
		n.next.extra = new([op2]unsafe.Pointer)
	}
	return n
}

func (n *int32nodeDesc) loadNext(i int) *int32nodeDesc {
	return (*int32nodeDesc)(n.next.load(i))
}

func (n *int32nodeDesc) storeNext(i int, next *int32nodeDesc) {
	n.next.store(i, unsafe.Pointer(next))
}

func (n *int32nodeDesc) atomicLoadNext(i int) *int32nodeDesc {
	return (*int32nodeDesc)(n.next.atomicLoad(i))
}

func (n *int32nodeDesc) atomicStoreNext(i int, next *int32nodeDesc) {
	n.next.atomicStore(i, unsafe.Pointer(next))
}

// findNodeRemove takes a value and two maximal-height arrays then searches exactly as in a sequential skip-list.
// The returned preds and succs always satisfy preds[i] > value >= succs[i].
func (s *Int32SetDesc) findNodeRemove(value int32, preds *[maxLevel]*int32nodeDesc, succs *[maxLevel]*int32nodeDesc) int {
	// lFound represents the index of the first layer at which it found a node.
	lFound, x := -1, s.header
	for i := int(atomic.LoadUint64(&s.highestLevel)) - 1; i >= 0; i-- {
		succ := x.atomicLoadNext(i)
		for succ != nil && (succ.value > value) {
			x = succ
			succ = x.atomicLoadNext(i)
		}
		preds[i] = x
		succs[i] = succ

		// Check if the value already in the skip list.
		if lFound == -1 && succ != nil && succ.value == value {
			lFound = i
		}
	}
	return lFound
}

// findNodeAdd takes a value and two maximal-height arrays then searches exactly as in a sequential skip-set.
// The returned preds and succs always satisfy preds[i] > value >= succs[i].
func (s *Int32SetDesc) findNodeAdd(value int32, preds *[maxLevel]*int32nodeDesc, succs *[maxLevel]*int32nodeDesc) int {
	x := s.header
	for i := int(atomic.LoadUint64(&s.highestLevel)) - 1; i >= 0; i-- {
		succ := x.atomicLoadNext(i)
		for succ != nil && (succ.value > value) {
			x = succ
			succ = x.atomicLoadNext(i)
		}
		preds[i] = x
		succs[i] = succ

		// Check if the value already in the skip list.
		if succ != nil && succ.value == value {
			return i
		}
	}
	return -1
}

func unlockint32Desc(preds [maxLevel]*int32nodeDesc, highestLevel int) {
	var prevPred *int32nodeDesc
	for i := highestLevel; i >= 0; i-- {
		if preds[i] != prevPred { // the node could be unlocked by previous loop
			preds[i].mu.Unlock()
			prevPred = preds[i]
		}
	}
}

// Add adds the value into skip set, returns true if this process insert the value into skip set,
// returns false if this process can't insert this value, because another process has insert the same value.
//
// If the value is in the skip set but not fully linked, this process will wait until it is.
func (s *Int32SetDesc) Add(value int32) bool {
	level := s.randomlevel()
	var preds, succs [maxLevel]*int32nodeDesc
	for {
		lFound := s.findNodeAdd(value, &preds, &succs)
		if lFound != -1 { // indicating the value is already in the skip-list
			nodeFound := succs[lFound]
			if !nodeFound.flags.Get(marked) {
				for !nodeFound.flags.Get(fullyLinked) {
					// The node is not yet fully linked, just waits until it is.
				}
				return false
			}
			// If the node is marked, represents some other thread is in the process of deleting this node,
			// we need to add this node in next loop.
			continue
		}
		// Add this node into skip list.
		var (
			highestLocked        = -1 // the highest level being locked by this process
			valid                = true
			pred, succ, prevPred *int32nodeDesc
		)
		for layer := 0; valid && layer < level; layer++ {
			pred = preds[layer]   // target node's previous node
			succ = succs[layer]   // target node's next node
			if pred != prevPred { // the node in this layer could be locked by previous loop
				pred.mu.Lock()
				highestLocked = layer
				prevPred = pred
			}
			// valid check if there is another node has inserted into the skip list in this layer during this process.
			// It is valid if:
			// 1. The previous node and next node both are not marked.
			// 2. The previous node's next node is succ in this layer.
			valid = !pred.flags.Get(marked) && (succ == nil || !succ.flags.Get(marked)) && pred.loadNext(layer) == succ
		}
		if !valid {
			unlockint32Desc(preds, highestLocked)
			continue
		}

		nn := newInt32NodeDesc(value, level)
		for layer := 0; layer < level; layer++ {
			nn.storeNext(layer, succs[layer])
			preds[layer].atomicStoreNext(layer, nn)
		}
		nn.flags.SetTrue(fullyLinked)
		unlockint32Desc(preds, highestLocked)
		atomic.AddInt64(&s.length, 1)
		return true
	}
}

func (s *Int32SetDesc) randomlevel() int {
	// Generate random level.
	level := randomLevel()
	// Update highest level if possible.
	for {
		hl := atomic.LoadUint64(&s.highestLevel)
		if level <= int(hl) {
			break
		}
		if atomic.CompareAndSwapUint64(&s.highestLevel, hl, uint64(level)) {
			break
		}
	}
	return level
}

// Contains checks if the value is in the skip set.
func (s *Int32SetDesc) Contains(value int32) bool {
	x := s.header
	for i := int(atomic.LoadUint64(&s.highestLevel)) - 1; i >= 0; i-- {
		nex := x.atomicLoadNext(i)
		for nex != nil && (nex.value > value) {
			x = nex
			nex = x.atomicLoadNext(i)
		}

		// Check if the value already in the skip list.
		if nex != nil && nex.value == value {
			return nex.flags.MGet(fullyLinked|marked, fullyLinked)
		}
	}
	return false
}

// Remove removes a node from the skip set.
func (s *Int32SetDesc) Remove(value int32) bool {
	var (
		nodeToRemove *int32nodeDesc
		isMarked     bool // represents if this operation mark the node
		topLayer     = -1
		preds, succs [maxLevel]*int32nodeDesc
	)
	for {
		lFound := s.findNodeRemove(value, &preds, &succs)
		if isMarked || // this process mark this node or we can find this node in the skip list
			lFound != -1 && succs[lFound].flags.MGet(fullyLinked|marked, fullyLinked) && (int(succs[lFound].level)-1) == lFound {
			if !isMarked { // we don't mark this node for now
				nodeToRemove = succs[lFound]
				topLayer = lFound
				nodeToRemove.mu.Lock()
				if nodeToRemove.flags.Get(marked) {
					// The node is marked by another process,
					// the physical deletion will be accomplished by another process.
					nodeToRemove.mu.Unlock()
					return false
				}
				nodeToRemove.flags.SetTrue(marked)
				isMarked = true
			}
			// Accomplish the physical deletion.
			var (
				highestLocked        = -1 // the highest level being locked by this process
				valid                = true
				pred, succ, prevPred *int32nodeDesc
			)
			for layer := 0; valid && (layer <= topLayer); layer++ {
				pred, succ = preds[layer], succs[layer]
				if pred != prevPred { // the node in this layer could be locked by previous loop
					pred.mu.Lock()
					highestLocked = layer
					prevPred = pred
				}
				// valid check if there is another node has inserted into the skip list in this layer
				// during this process, or the previous is removed by another process.
				// It is valid if:
				// 1. the previous node exists.
				// 2. no another node has inserted into the skip list in this layer.
				valid = !pred.flags.Get(marked) && pred.loadNext(layer) == succ
			}
			if !valid {
				unlockint32Desc(preds, highestLocked)
				continue
			}
			for i := topLayer; i >= 0; i-- {
				// Now we own the nodeToRemove, no other goroutine will modify it.
				// So we don't need nodeToRemove.loadNext
				preds[i].atomicStoreNext(i, nodeToRemove.loadNext(i))
			}
			nodeToRemove.mu.Unlock()
			unlockint32Desc(preds, highestLocked)
			atomic.AddInt64(&s.length, -1)
			return true
		}
		return false
	}
}

// Range calls f sequentially for each value present in the skip set.
// If f returns false, range stops the iteration.
func (s *Int32SetDesc) Range(f func(value int32) bool) {
	x := s.header.atomicLoadNext(0)
	for x != nil {
		if !x.flags.MGet(fullyLinked|marked, fullyLinked) {
			x = x.atomicLoadNext(0)
			continue
		}
		if !f(x.value) {
			break
		}
		x = x.atomicLoadNext(0)
	}
}

// RangeFrom calls f sequentially for all values with `value >= start` in the skip set.
// If f returns false, range stops the iteration.
func (s *Int32SetDesc) RangeFrom(start int32, f func(value int32) bool) {
	var (
		x   = s.header
		nex *int32nodeDesc
	)
	for i := int(atomic.LoadUint64(&s.highestLevel)) - 1; i >= 0; i-- {
		nex = x.atomicLoadNext(i)
		for nex != nil && (nex.value > start) {
			x = nex
			nex = x.atomicLoadNext(i)
		}
		// Check if the value already in the skip list.
		if nex != nil && nex.value == start {
			break
		}
	}

	for nex != nil {
		if !nex.flags.MGet(fullyLinked|marked, fullyLinked) {
			nex = nex.atomicLoadNext(0)
			continue
		}
		if !f(nex.value) {
			break
		}
		nex = nex.atomicLoadNext(0)
	}
}

// Len returns the length of this skip set.
func (s *Int32SetDesc) Len() int {
	return int(atomic.LoadInt64(&s.length))
}
