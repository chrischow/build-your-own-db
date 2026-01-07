package free

import (
	"encoding/binary"

	"cc.io/redisk/tree"
)

const (
	LNODE_HEADER = 8
	LNODE_CAP    = (tree.BTREE_PAGE_SIZE - LNODE_HEADER) / 8
)

type LNode []byte

func (node LNode) getNext() uint64 {
	return binary.LittleEndian.Uint64(node[0:])
}

func (node LNode) setNext(next uint64) {
	binary.LittleEndian.PutUint64(node[0:], next)
}

func (node LNode) getPointer(idx int) uint64 {
	startPos := (1 + idx) * 8
	return binary.LittleEndian.Uint64(node[startPos:])
}

func (node LNode) setPointer(idx int, pointer uint64) {
	startPos := (1 + idx) * 8
	binary.LittleEndian.PutUint64(node[startPos:], pointer)
}
