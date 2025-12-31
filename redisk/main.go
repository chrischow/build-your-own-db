package main

import (
	"fmt"

	"cc.io/redisk/kvstore"
)

func main() {
	kv := kvstore.KV{Path: "/Users/chrischow/build-your-own-db/redisk/tmp"}
	if err := kv.Open(); err != nil {
		fmt.Printf("error: %v\n", err)
		panic("error")
	}

	// kv.Set([]byte("a"), []byte("b"))
	kv.Inspect()

	val, ok := kv.Get([]byte("a"))
	if ok {
		fmt.Printf("Value: %s\n", string(val))
	}
}
