package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
	fmt.Println(add(1, 2))
}

func add(x, y int) int {
	return x + y
}
