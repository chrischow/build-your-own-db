package free

import (
	"cc.io/redisk/tree"
	"cc.io/redisk/utils"
)

type FreeList struct {
	// Calbacks to manage on-disk pages
	get func(uint64) []byte // Read a page
	new func([]byte) uint64 // Append a new page
	set func(uint64) []byte // Update an existing page

	// Persisted data in the meta page
	headPage uint64 // Point to LL head
	headSeq  uint64 // Monotonic sequence number to index into the list head
	tailPage uint64
	tailSeq  uint64

	// In-memory states
	maxSeq uint64 // Saved tailSeq to prevent consuming newly added items
}

// Get 1 item from the list head. Returns 0 on failure.
func (fl *FreeList) PopHead() uint64 {
	pointer, head := flPop(fl)
	if head != 0 {
		fl.PushTail(head)
	}
	return pointer
}

// Add 1 item to the tail
func (fl *FreeList) PushTail(pointer uint64) {
	// Add to the tail node
	LNode(fl.set(fl.tailPage)).setPointer(seq2idx(fl.tailSeq), pointer)
	fl.tailSeq++

	// Add a new tail node if it's full
	if seq2idx(fl.tailSeq) == 0 {
		// Try to reuse from list head
		next, head := flPop(fl)
		if next == 0 {
			// Allocate a new node by appending
			next = fl.new(make([]byte, tree.BTREE_PAGE_SIZE))
		}
		// Link to new tail node
		LNode(fl.set(fl.tailPage)).setNext(next)
		fl.tailPage = next
		// Add head node if removed
		if head != 0 {
			LNode(fl.set(fl.tailPage)).setPointer(0, head)
			fl.tailSeq++
		}
	}
}

// Make newly added items available for consumption
func (fl *FreeList) SetMaxSeq() {
	fl.maxSeq = fl.tailSeq
}

// Remove 1 item from head, and remove head if empty
func flPop(fl *FreeList) (pointer uint64, head uint64) {
	if fl.headSeq == fl.maxSeq {
		return 0, 0
	}

	page := fl.get(fl.headPage)
	node := LNode(page)
	pointer = node.getPointer(seq2idx(fl.headSeq))
	fl.headSeq++

	// Move to next if head node is empty
	if seq2idx(fl.headSeq) == 0 {
		head, fl.headPage = fl.headPage, node.getNext()
		utils.Assert(fl.headPage != 0, "head page is empty")
	}

	return
}
