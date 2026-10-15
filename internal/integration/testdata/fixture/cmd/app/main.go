package main

import (
	"fmt"

	"example.com/fixture/greet"
)

func main() {
	fmt.Println(greet.Hello("Alice"))
	fmt.Println(greet.Hello("Bob"))
	fmt.Println(greet.Goodbye("Alice"))
}
