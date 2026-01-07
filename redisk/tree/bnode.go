package tree

import (
	"bytes"
	"encoding/binary"
	"errors"

	"cc.io/redisk/utils"
)

const (
	BNODE_NODE         = 1    // internal nodes
	BNODE_LEAF         = 2    // leaf nodes
	HEADER             = 4    // header size
	BTREE_PAGE_SIZE    = 4096 // node size, aligned to typical OS page size
	BTREE_MAX_KEY_SIZE = 1000 // key size is limited to fit into a node
	BTREE_MAX_VAL_SIZE = 3000 // value size is limited to fit into a noe
)

type BNode []byte

// Getter for the node type - 2 bytes
func (node BNode) bType() uint16 {
	return binary.LittleEndian.Uint16(node[0:2])
}

// Getter for the number of keys - 2 bytes
func (node BNode) nKeys() uint16 {
	return binary.LittleEndian.Uint16(node[2:4])
}

// Setter for the node type and number of keys
func (node BNode) setHeader(btype, nkeys uint16) {
	binary.LittleEndian.PutUint16(node[0:2], btype)
	binary.LittleEndian.PutUint16(node[2:4], nkeys)
}

// Get child pointers array
func (node BNode) getPointer(idx uint16) uint64 {
	utils.Assert(idx < node.nKeys(), "cannot get pointer: index out of range")
	pos := 4 + 8*idx
	return binary.LittleEndian.Uint64(node[pos:])
}

// Write child pointers array
func (node BNode) setPointer(idx uint16, val uint64) {
	utils.Assert(idx < node.nKeys(), "cannot set pointer: index out of range")
	pos := 4 + 8*idx
	binary.LittleEndian.PutUint64(node[pos:], val)
}

// Read offsets array
func (node BNode) getOffset(idx uint16) uint16 {
	if idx == 0 {
		return 0
	}
	pos := 4 + 8*node.nKeys() + 2*(idx-1)
	return binary.LittleEndian.Uint16(node[pos:])
}

// Set offset
func (node BNode) setOffset(idx, offset uint16) {
	nKeys := node.nKeys()
	pos := uint16(0)
	if idx > 0 {
		pos = 4 + 8*nKeys + 2*(idx-1)
	}
	binary.LittleEndian.PutUint16(node[pos:], offset)
}

// Read KV position
func (node BNode) kvPos(idx uint16) uint16 {
	nKeys := node.nKeys()
	utils.Assert(idx <= nKeys, "cannot get key-value position: index out of range")
	// 4 bytes header + 8 bytes pointers + 2 bytes per offset
	return 4 + 8*nKeys + 2*nKeys + node.getOffset(idx)
}

// Read key
func (node BNode) getKey(idx uint16) []byte {
	utils.Assert(idx < node.nKeys(), "cannot get key: index out of range")
	pos := node.kvPos(idx)
	keyLen := binary.LittleEndian.Uint16(node[pos:])
	return node[pos+4:][:keyLen]
}

// Read value
func (node BNode) getValue(idx uint16) []byte {
	utils.Assert(idx < node.nKeys(), "cannot get value: index out of range")
	pos := node.kvPos(idx)
	keyLen := binary.LittleEndian.Uint16(node[pos+0:])
	valueLen := binary.LittleEndian.Uint16(node[pos+2:])
	return node[pos+4+keyLen:][:valueLen]
}

// Get node size in bytes
func (node BNode) nBytes() uint16 {
	return node.kvPos(node.nKeys())
}

// Creates an empty node
func CreateNode() BNode {
	return BNode(make([]byte, BTREE_PAGE_SIZE))
}

// Appends key-value, using the latest value of the offsets array
func nodeAppendKeyValue(node BNode, idx uint16, ptr uint64, key, val []byte) {
	// Set pointers
	node.setPointer(idx, ptr)

	// Get the latest offset
	pos := node.kvPos(idx)

	// Updates KV sizes
	keyLen := uint16(len(key))
	valLen := uint16(len(val))
	binary.LittleEndian.PutUint16(node[pos+0:], keyLen)
	binary.LittleEndian.PutUint16(node[pos+2:], valLen)

	// KV data
	copy(node[pos+4:], key)
	copy(node[pos+4+keyLen:], val)

	// Update offset
	node.setOffset(idx+1, node.getOffset(idx)+4+keyLen+valLen)
}

// Copy keys, values, and pointers
func nodeAppendRange(
	newNode, oldNode BNode,
	destIdxInNewNode, srcIdxInOldNode, numIndices uint16,
) {
	for i := range numIndices {
		dst, src := destIdxInNewNode+i, srcIdxInOldNode+i
		nodeAppendKeyValue(
			newNode,
			dst,
			oldNode.getPointer(src),
			oldNode.getKey(src),
			oldNode.getValue(src),
		)
	}
}

// Insert a new leaf node
func leafInsert(
	newNode, oldNode BNode, idx uint16, key, val []byte,
) {
	newNode.setHeader(BNODE_LEAF, oldNode.nKeys()+1)
	// Copy keys before the idx
	nodeAppendRange(newNode, oldNode, 0, 0, idx)
	nodeAppendKeyValue(newNode, idx, 0, key, val)
	nodeAppendRange(newNode, oldNode, idx+1, idx, oldNode.nKeys()-idx)
}

// Update an existing key
func leafUpdate(
	newNode, oldNode BNode, idx uint16, key, val []byte,
) {
	newNode.setHeader(BNODE_LEAF, oldNode.nKeys())
	nodeAppendRange(newNode, oldNode, 0, 0, idx)
	nodeAppendKeyValue(newNode, idx, 0, key, val)
	nodeAppendRange(newNode, oldNode, idx+1, idx+1, oldNode.nKeys()-(idx+1))
}

// Find the last index that is less than or equal to the key
func nodeLookupLessThanOrEqual(node BNode, key []byte) uint16 {
	nKeys := node.nKeys()
	var i uint16

	for i = 0; i < nKeys; i++ {
		compareResult := bytes.Compare(node.getKey(i), key)

		if compareResult == 0 {
			return i
		}

		if compareResult > 0 {
			return i - 1
		}
	}

	return i - 1
}

// Split oversized node into 2 nodes
func nodeSplitIntoTwo(left, right, old BNode) {
	utils.Assert(old.nKeys() >= uint16(2), "cannot split node with 2 or fewer keys")

	// Initial guess
	nLeftKeys := old.nKeys() / 2

	// Start by fitting the left - use this as a starting point
	getLeftBytes := func() uint16 {
		return 4 + 8*nLeftKeys + 2*nLeftKeys + old.getOffset(nLeftKeys)
	}

	for getLeftBytes() < BTREE_PAGE_SIZE {
		nLeftKeys--
	}

	utils.Assert(nLeftKeys >= 1, "could not split node: left contains no keys")

	// Ensure that the new node does not exceed page size
	getRightBytes := func() uint16 {
		return old.nBytes() - getLeftBytes() + 4
	}

	for getRightBytes() > BTREE_PAGE_SIZE {
		nLeftKeys++
	}

	utils.Assert(nLeftKeys < old.nKeys(), "could not split node: left index out of range")

	nRightKeys := old.nKeys() - nLeftKeys

	// Create new nodes
	left.setHeader(old.bType(), nLeftKeys)
	nodeAppendRange(left, old, 0, 0, nLeftKeys)

	right.setHeader(old.bType(), nRightKeys)
	nodeAppendRange(right, old, 0, nLeftKeys, nRightKeys)

	// It's ok for the left node to not be checked:
	// 1. Can split again
	// 2. Since the insertion caused the overflow and the left was already within limits,
	//    it's likely that splitting will keep the left within limits or only just barely overflow
	utils.Assert(right.nBytes() <= BTREE_PAGE_SIZE, "failed to split node: right node is too big")
}

func nodeSplit(old BNode) (uint16, [3]BNode) {
	if old.nBytes() <= BTREE_PAGE_SIZE {
		old = old[:BTREE_PAGE_SIZE]
		return 1, [3]BNode{old}
	}

	left := BNode(make([]byte, 2*BTREE_PAGE_SIZE))
	right := CreateNode()
	nodeSplitIntoTwo(left, right, old)

	// If both nodes are within size
	if left.nBytes() <= BTREE_PAGE_SIZE {
		left = left[:BTREE_PAGE_SIZE]
		return 2, [3]BNode{left, right}
	}

	// Resplit the left
	leftLeft := CreateNode()
	middle := CreateNode()
	nodeSplitIntoTwo(leftLeft, middle, left)
	utils.Assert(leftLeft.nBytes() <= BTREE_PAGE_SIZE, "failed to split node: first node exceeds page size")

	return 3, [3]BNode{leftLeft, middle, right}
}

// Checks that the key and value are within size limits
func checkLimit(key, val []byte) error {
	if len(key) > BTREE_MAX_KEY_SIZE {
		return errors.New("key size exceeds max allowed size")
	}

	if len(val) > BTREE_MAX_VAL_SIZE {
		return errors.New("value size exceeds max allowed size")
	}

	return nil
}

// Remove a key from a leaf node
func leafDelete(newNode, oldNode BNode, idx uint16) {
	newNode.setHeader(BNODE_LEAF, oldNode.nKeys()-1)
	nodeAppendRange(newNode, oldNode, 0, 0, idx)
	nodeAppendRange(newNode, oldNode, idx, idx+1, oldNode.nKeys()-(idx+1))
}

// Merges 2 nodes into 1
func nodeMerge(newNode, leftNode, rightNode BNode) {
	newNode.setHeader(leftNode.bType(), leftNode.nKeys()+rightNode.nKeys())
	nodeAppendRange(newNode, leftNode, 0, 0, leftNode.nKeys())
	nodeAppendRange(newNode, rightNode, leftNode.nKeys(), 0, rightNode.nKeys())
}

// Replace 2 adjacent links with 1
func nodeReplaceTwoChildren(
	newNode, oldNode BNode, idx uint16, pointer uint64, key []byte,
) {
	newNode.setHeader(BNODE_NODE, oldNode.nKeys()-1)
	// Add previous links
	nodeAppendRange(newNode, oldNode, 0, 0, idx)

	// Add new link
	nodeAppendKeyValue(newNode, idx, pointer, key, nil)

	// Add subsequent links, if any
	if idx+2 < oldNode.nKeys() {
		nodeAppendRange(newNode, oldNode, idx+1, idx+2, oldNode.nKeys()-(idx+2))
	}
}
