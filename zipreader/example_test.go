package zipreader_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"

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

// Read the content of an entry. The size and the CRC-32 are checked while the content is read: only a read
// that ends with io.EOF (here inside io.ReadAll) confirms that the content is intact.
func ExampleReader_Open() {
	data := testkit.Zip(
		testkit.File{Name: "word/document.xml", Data: []byte("<w:document/>"), Method: 8},
	).Bytes()

	r, err := zipreader.New(bytes.NewReader(data), int64(len(data)), zipreader.Options{})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, e := range r.Entries() {
		rc, err := r.Open(e)
		if err != nil {
			fmt.Println(err) // for example unsupported-encryption or limit-entry-size
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			fmt.Println(err) // for example zip-checksum
			continue
		}
		fmt.Printf("%s: %s\n", e.Name, content)
	}
	// Output:
	// word/document.xml: <w:document/>
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
