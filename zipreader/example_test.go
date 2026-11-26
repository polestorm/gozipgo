package zipreader_test

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/polestorm/gozipgo/internal/testkit"
	"github.com/polestorm/gozipgo/zipreader"
)

// List the entries of an archive. The zero Options is strict and has every limit at its default.
func ExampleNew() {
	// An archive held in memory; any io.ReaderAt with a known size will do, for example an *os.File.
	data := testkit.Zip(
		testkit.File{Name: "word/"},
		testkit.File{Name: "word/document.xml", Data: []byte("<w:document/>"), Method: 8},
	).Bytes()

	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range r.Entries() {
		fmt.Printf("%d %s dir=%v method=%d size=%d\n", e.Index, e.Name, e.IsDir, e.Method, e.UncompressedSize)
	}
	// Output:
	// 0 word/ dir=true method=0 size=0
	// 1 word/document.xml dir=false method=8 size=13
}

// An archive that breaks a rule is rejected with an *Error that carries a fixed code.
func ExampleError() {
	data := testkit.Zip(testkit.File{Name: "../outside.txt"}).Bytes()

	_, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{})
	var e *zipreader.Error
	if errors.As(err, &e) {
		fmt.Println(e.Code, e.Entry, e.Name)
	}
	// Output:
	// name-traversal 0 ../outside.txt
}
