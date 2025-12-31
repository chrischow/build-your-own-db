package utils

import "fmt"

func Assert(condition bool, message string) {
	if condition {
		return
	}
	if message != "" {
		fmt.Println(message)
	}
	fmt.Println("assertion failed: condition failed")
}
