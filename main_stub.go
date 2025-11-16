//go:build !fuse

package main

import (
	"log"
)

func main() {
	log.Fatal("This binary was built without FUSE support. Please rebuild with: go build -tags fuse")
}
