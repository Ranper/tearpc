package tearpc

import (
	"bytes"
	"encoding/gob"
	"log"
	"testing"
)

func call1() {
	log.Println("call1")
}

type s1 struct {
	Name string
}

type s2 struct {
	Int   int
	Float float32
}

func TestDefer(t *testing.T) {
	defer log.Println("TestDefer exit")
	call1()
}

func TestEncodeDecode(t *testing.T) {
	var v1 = s1{
		Name: "testName",
	}
	data := []byte{}
	stm := bytes.NewBuffer(data)
	enc := gob.NewEncoder(stm)
	enc.Encode(v1)

	log.Println("enc data: ", stm)
	// var v11 s1
	var v2 s2
	gob.NewDecoder(stm).Decode(&v2)
	log.Println("decode res: ", v2)
	log.Println("after decode: ", stm)
}
