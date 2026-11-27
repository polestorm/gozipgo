package zipwriter

import "strconv"

// Error codes of the writer (Error.Code). A code, once released, keeps its meaning and is not removed. The
// catalogue is docs/errors.md.
//
// The "name-*" codes are the codes the reader gives for the same names: what zipreader rejects, the writer
// refuses to write. "write-*" are misuse of the writer and content it cannot record.
const (
	CodeNameEmpty     = "name-empty"             // an entry without a name
	CodeNameNUL       = "name-nul"               // a zero byte in a name
	CodeNameEncoding  = "name-encoding"          // a name that is not valid UTF-8
	CodeNameControl   = "name-control-character" // a control character in a name
	CodeNameBackslash = "name-backslash"         // a backslash in a name
	CodeNameAbsolute  = "name-absolute"          // a leading slash or a drive letter
	CodeNameTraversal = "name-traversal"         // a "..", "." or empty path segment
	CodeNameDuplicate = "name-duplicate"         // two entries that cannot both exist in one tree

	CodeNameLength       = "write-name-length"       // a name longer than 65,535 bytes, the limit of the format
	CodeMethod           = "write-method"            // a compression method other than Stored and Deflate
	CodeTimestamp        = "write-timestamp"         // a date or time the format cannot record
	CodeDirectoryContent = "write-directory-content" // content for an entry whose name ends with "/"
	CodeEntryOpen        = "write-entry-open"        // the previous entry has not been closed
	CodeAfterClose       = "write-after-close"       // use of the writer, or of an entry, after its Close
	CodeEntrySize        = "write-entry-size"        // a streamed entry reached 4 GiB without Header.Zip64
	CodeRawContent       = "write-raw-content"       // raw data that are not what their description says
	CodeSelfCheck        = "write-self-check"        // the compressor wrote data that do not decode to the content
)

// Error is an error of the writer about what it was asked to write. Errors of the io.Writer the archive is
// written to, and of the io.Reader given to AddRaw, are not wrapped in it: they are returned as they are.
type Error struct {
	Code string // one of the Code* constants
	// Entry is the index the entry has, or would have had, in the archive (entries are numbered in the order of
	// the calls), or -1 when the error is not about an entry.
	Entry int
	// Name is the name of the entry as given by the caller.
	Name string
	// Message describes the error in English. It is not part of the contract and may change between versions.
	Message string
}

// Error returns the code, the entry (when the error is about one) and the message.
func (e *Error) Error() string {
	s := "zip: " + e.Code
	if e.Entry >= 0 {
		s += " (entry " + strconv.Itoa(e.Entry)
		if e.Name != "" {
			s += " " + strconv.Quote(e.Name)
		}
		s += ")"
	}
	return s + ": " + e.Message
}
