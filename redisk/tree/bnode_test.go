package tree

import (
	"reflect"
	"testing"
)

type Kv struct {
	Key string
	Val string
}

func TestOps(t *testing.T) {
	t.Run("it should successfully add KVs", func(t *testing.T) {
		// Instantiate node
		node := createDefaultNode()

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k2", Val: "a"},
			{Key: "k3", Val: "hello"},
		}

		assertKeysAndValues(t, node, cases)
	})

	t.Run("it should copy range", func(t *testing.T) {
		// Instantiate node
		node := BNode(make([]byte, BTREE_PAGE_SIZE))

		// Create a leaf node with 2 keys
		node.setHeader(BNODE_LEAF, 2)
		nodeAppendKeyValue(node, 0, 0, []byte("k1"), []byte("hi"))
		nodeAppendKeyValue(node, 1, 0, []byte("k2"), []byte("a"))

		// Create a new leaf node with 2 keys
		newNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		newNode.setHeader(BNODE_LEAF, 2)
		nodeAppendRange(newNode, node, 0, 0, 2)

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k2", Val: "a"},
		}

		assertKeysAndValues(t, newNode, cases)
	})

	t.Run("it should insert node at index 1", func(t *testing.T) {
		// Create a leaf node with 3 keys
		oldNode := createDefaultNode()

		// Create new leaf node with 4 keys
		newNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		newNode.setHeader(BNODE_LEAF, oldNode.nKeys()+1)
		leafInsert(newNode, oldNode, 1, []byte("k"), []byte("inserted"))

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k", Val: "inserted"},
			{Key: "k2", Val: "a"},
			{Key: "k3", Val: "hello"},
		}

		assertKeysAndValues(t, newNode, cases)
	})

	t.Run("it should update node at index 1", func(t *testing.T) {
		// Create a leaf node with 3 keys
		oldNode := createDefaultNode()

		// Create new leaf node with 4 keys
		newNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		newNode.setHeader(BNODE_LEAF, oldNode.nKeys())
		leafUpdate(newNode, oldNode, 1, []byte("k2"), []byte("updated"))

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k2", Val: "updated"},
			{Key: "k3", Val: "hello"},
		}

		assertKeysAndValues(t, newNode, cases)
	})

	t.Run("it should return correct index of existing node", func(t *testing.T) {
		// Create a leaf node with 3 keys
		node := createDefaultNode()

		got := nodeLookupLessThanOrEqual(node, []byte("k2"))
		want := uint16(1)

		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	})

	t.Run("it should return index before next key", func(t *testing.T) {
		// Create a leaf node with 3 keys
		node := createDefaultNode()

		got := nodeLookupLessThanOrEqual(node, []byte("k11"))
		want := uint16(0)

		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	})

	t.Run("it should delete a key", func(t *testing.T) {
		oldNode := createDefaultNode()

		newNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		leafDelete(newNode, oldNode, 1)

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k3", Val: "hello"},
		}

		assertKeysAndValues(t, newNode, cases)
	})

	t.Run("it should merge two leaf nodes into one", func(t *testing.T) {
		node1 := createDefaultNode()
		node2 := createDefaultNode()

		mergedNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		nodeMerge(mergedNode, node1, node2)

		cases := []Kv{
			{Key: "k1", Val: "hi"},
			{Key: "k2", Val: "a"},
			{Key: "k3", Val: "hello"},
			{Key: "k1", Val: "hi"},
			{Key: "k2", Val: "a"},
			{Key: "k3", Val: "hello"},
		}

		assertKeysAndValues(t, mergedNode, cases)
	})

	t.Run("it should merge two non-leaf nodes into one", func(t *testing.T) {
		node := BNode(make([]byte, BTREE_PAGE_SIZE))
		node.setHeader(BNODE_LEAF, 4)
		nodeAppendKeyValue(node, 0, 0, []byte("1"), []byte(nil))
		nodeAppendKeyValue(node, 1, 0, []byte("2"), []byte(nil))
		nodeAppendKeyValue(node, 2, 0, []byte("3"), []byte(nil))
		nodeAppendKeyValue(node, 3, 0, []byte("4"), []byte(nil))

		newNode := BNode(make([]byte, BTREE_PAGE_SIZE))
		nodeReplaceTwoChildren(newNode, node, 1, 0, []byte("x"))

		cases := []Kv{
			{Key: "1", Val: ""},
			{Key: "x", Val: ""},
			{Key: "4", Val: ""},
		}

		assertKeysAndValues(t, newNode, cases)

		nKeys := uint16(len(cases))
		if newNode.nKeys() != nKeys {
			t.Errorf("got %d keys, want %d keys", newNode.nKeys(), nKeys)
		}
	})
}

func createDefaultNode() BNode {
	// Create a leaf node with 3 keys
	node := BNode(make([]byte, BTREE_PAGE_SIZE))
	node.setHeader(BNODE_LEAF, 3)
	nodeAppendKeyValue(node, 0, 0, []byte("k1"), []byte("hi"))
	nodeAppendKeyValue(node, 1, 0, []byte("k2"), []byte("a"))
	nodeAppendKeyValue(node, 2, 0, []byte("k3"), []byte("hello"))

	return node
}

func assertKeysAndValues(t testing.TB, node BNode, kvs []Kv) {
	t.Helper()

	for i, c := range kvs {
		idx := uint16(i)
		got := Kv{
			Key: string(node.getKey(idx)),
			Val: string(node.getValue(idx)),
		}

		if !reflect.DeepEqual(got.Key, c.Key) {
			t.Errorf("key: got %q, want %q", got.Key, c.Key)
		}

		if !reflect.DeepEqual(got.Val, c.Val) {
			t.Errorf("value: got %q, want %q", got.Val, c.Val)
		}
	}
}
