package tree

import (
	"bytes"
	"errors"

	"cc.io/redisk/utils"
)

type BTree struct {
	// Root pointer i.e. a non-zero page number
	Root uint64

	// Callbacks for managing on-disk pages
	// Read data from a page number
	Get func(uint64) []byte

	// Allocate a New page number with data
	New func([]byte) uint64

	// Deallocate a page number
	Del func(uint64)
}

func (tree *BTree) Find(key []byte) ([]byte, bool) {
	node := BNode(tree.Get(tree.Root))

	// Get the index
	idx := nodeLookupLessThanOrEqual(node, key)

	switch node.bType() {
	case BNODE_LEAF:
		if !bytes.Equal(key, node.getKey(idx)) {
			// Not found
			return nil, false
		}
		return node.getValue(idx), true

	case BNODE_NODE:
		return tree.Find(key)
	default:
		panic("bad node")
	}
}

// Replace a link with multiple links
func nodeReplaceChildrenN(
	tree *BTree, newNode, oldNode BNode, idx uint16, childNodes ...BNode,
) {
	numNewLinks := uint16(len(childNodes))

	newNode.setHeader(BNODE_NODE, oldNode.nKeys()+numNewLinks-1)
	// Copy over links before the index
	nodeAppendRange(newNode, oldNode, 0, 0, idx)

	// Replace the index
	for i, node := range childNodes {
		// Set pointers and only the key
		nodeAppendKeyValue(newNode, idx+uint16(i), tree.New(node), node.getKey(0), nil)
	}

	// Copy over links after the index
	nodeAppendRange(newNode, oldNode, idx+numNewLinks, idx+1, oldNode.nKeys()-(idx+1))
}

// Inserts a new or key or updates an existing key
func (tree *BTree) Insert(key, val []byte) error {
	// Check key/val size
	if err := checkLimit(key, val); err != nil {
		return err
	}

	// Create first node
	if tree.Root == 0 {
		root := CreateNode()
		root.setHeader(BNODE_LEAF, 2)
		// Sentinel value: a dummy key to make the tree cover the whole key space
		nodeAppendKeyValue(root, 0, 0, nil, nil)
		nodeAppendKeyValue(root, 1, 0, key, val)
		tree.Root = tree.New(root)
		return nil
	}

	// Insert the key
	node := treeInsert(tree, tree.Get(tree.Root), key, val)

	// Grow the tree if the root is split
	numSplits, splitNodes := nodeSplit(node)
	tree.Del(tree.Root)

	if numSplits > 1 {
		root := CreateNode()
		root.setHeader(BNODE_NODE, numSplits)

		for i, childNode := range splitNodes[:numSplits] {
			// Use the first key to identify the child node in its parent
			pointer, key := tree.New(childNode), childNode.getKey(0)
			nodeAppendKeyValue(root, uint16(i), pointer, key, nil)
		}
		tree.Root = tree.New(root)
	} else {
		tree.Root = tree.New(splitNodes[0])
	}

	return nil
}

// Deletes a key and returns whether the key was there
func (tree *BTree) Delete(key []byte) (bool, error) {
	utils.Assert(len(key) != 0, "could not delete key: zero length")
	checkLimit(key, nil)

	if tree.Root == 0 {
		return false, errors.New("could not delete key: root does not exit")
	}

	updatedNode := treeDelete(tree, tree.Get(tree.Root), key)
	if len(updatedNode) == 0 {
		return false, errors.New("could not delete key: not found")
	}

	tree.Del(tree.Root)
	if updatedNode.bType() == BNODE_NODE && updatedNode.nKeys() == 1 {
		// Flatten
		tree.Root = updatedNode.getPointer(0)
	} else {
		tree.Root = tree.New(updatedNode)
	}

	return true, nil
}

// Inserts a new key/value into a tree
func treeInsert(tree *BTree, node BNode, key, val []byte) BNode {
	// Copy-on-write: Create a new node
	newNode := BNode(make([]byte, 2*BTREE_PAGE_SIZE))

	// Locate place to insert/update key
	idx := nodeLookupLessThanOrEqual(node, key)

	switch node.bType() {
	case BNODE_LEAF:
		if bytes.Equal(key, node.getKey(idx)) {
			// Update
			leafUpdate(newNode, node, idx, key, val)
		} else {
			// Insert
			leafInsert(newNode, node, idx+1, key, val)
		}
	case BNODE_NODE:
		childPointer := node.getPointer(idx)
		childNode := treeInsert(tree, tree.Get(childPointer), key, val)
		// Split after insertion
		numSplitNodes, splitNodes := nodeSplit(childNode)

		// Deallocate old child node
		tree.Del(childPointer)

		// Update child links
		nodeReplaceChildrenN(tree, newNode, node, idx, splitNodes[:numSplitNodes]...)
	}

	return newNode
}

// Checks whether the updated child should be merged with a sibling. 0 = no merge. -1 = merge with left. +2 = merge with right.
func shouldMerge(
	tree *BTree, parentNode, updatedNode BNode, updatedNodeIdx uint16,
) (int, BNode) {
	// Use a threshold > 0 to decide whether to merge
	if updatedNode.nBytes() > BTREE_PAGE_SIZE/4 {
		return 0, BNode{}
	}

	if updatedNodeIdx > 0 {
		leftSibling := BNode(tree.Get(parentNode.getPointer(updatedNodeIdx - 1)))
		mergedBytes := leftSibling.nBytes() + updatedNode.nBytes() - HEADER
		if mergedBytes <= BTREE_PAGE_SIZE {
			return -1, leftSibling
		}
	}

	if updatedNodeIdx+1 < parentNode.nKeys() {
		rightSibling := BNode(tree.Get(parentNode.getPointer(updatedNodeIdx + 1)))
		mergedBytes := rightSibling.nBytes() + updatedNode.nBytes() - HEADER
		if mergedBytes <= BTREE_PAGE_SIZE {
			return +1, rightSibling
		}
	}

	return 0, BNode{}
}

// Deletes a key from a tree
func treeDelete(tree *BTree, node BNode, key []byte) BNode {
	// Get the index
	idx := nodeLookupLessThanOrEqual(node, key)

	switch node.bType() {
	case BNODE_LEAF:
		if !bytes.Equal(key, node.getKey(idx)) {
			// Not found
			return BNode{}
		}

		newNode := CreateNode()
		leafDelete(newNode, node, idx)
		return newNode
	case BNODE_NODE:
		return nodeDelete(tree, node, idx, key)
	default:
		panic("bad node")
	}
}

// Delete a key from an internal node by index
func nodeDelete(tree *BTree, node BNode, idx uint16, key []byte) BNode {
	childPointer := node.getPointer(idx)
	updatedNode := treeDelete(tree, tree.Get(childPointer), key)

	// No longer found
	if len(updatedNode) == 0 {
		return BNode{}
	}

	// Remove pointer to outdated node
	tree.Del(childPointer)

	// Check if the node with the updated pointers / keys should be merged
	mergeDirection, sibling := shouldMerge(tree, node, updatedNode, idx)

	newNode := CreateNode()

	switch {
	case mergeDirection < 0:
		// Merge with left
		mergedNode := CreateNode()
		nodeMerge(mergedNode, sibling, updatedNode)

		// Remove pointer to left
		tree.Del(node.getPointer(idx - 1))

		// Update parent node's pointers
		nodeReplaceTwoChildren(newNode, node, idx-1, tree.New(mergedNode), mergedNode.getKey(0))
	case mergeDirection > 0:
		// Merge with right
		mergedNode := CreateNode()
		nodeMerge(mergedNode, updatedNode, sibling)

		// Remove pointer to right
		tree.Del(node.getPointer(idx + 1))

		// Update parent node's pointers
		nodeReplaceTwoChildren(newNode, node, idx, tree.New(mergedNode), mergedNode.getKey(0))
	case mergeDirection == 0 && updatedNode.nKeys() == 0:
		// Child is empty, so the parent should be empty
		newNode.setHeader(BNODE_NODE, 0)
	case mergeDirection == 0 && updatedNode.nKeys() > 0:
		// Update parent node's pointers: 1 for 1
		nodeReplaceChildrenN(tree, newNode, node, idx, updatedNode)
	}

	return newNode
}
