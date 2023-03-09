package tearpc

import (
	"log"
	"testing"
)

func call1() {
	log.Println("call1")
}

func TestDefer(t *testing.T) {
	defer log.Println("TestDefer exit")
	call1()
}
