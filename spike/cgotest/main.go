package main

/*
#include <stdio.h>

void hello() {
    printf("Hello from CGO on Windows!\n");
}
*/
import "C"
import "fmt"

func main() {
	fmt.Println("Starting CGO test...")
	C.hello()
	fmt.Println("Completed CGO test.")
}
