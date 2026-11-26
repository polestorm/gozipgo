package zipreader

import "strconv"

// Error codes of the reader (Error.Code). A code, once released, keeps its meaning and is not removed, so a caller
// can classify and translate errors by code. The catalogue with the rule of the specification behind each code is
// docs/errors.md.
//
// "zip-*" and "zip64-*" are violations of the format or structures of the archive that contradict each other;
// "name-*" are entry names the reader does not accept; "limit-*" are the limits of the reader (Options);
// "unsupported-*" are valid features outside the scope of the library.
const (
	CodeNoDirectoryEnd         = "zip-no-directory-end"         // no end of central directory record that ends with the file
	CodeMultipleDirectoryEnds  = "zip-multiple-directory-ends"  // more than one record could be the end of central directory
	CodeTrailingData           = "zip-trailing-data"            // bytes after the comment of the end of central directory record
	CodeDirectoryOutOfBounds   = "zip-directory-out-of-bounds"  // the central directory does not end where the end records begin
	CodeDirectoryCountMismatch = "zip-directory-count-mismatch" // number or total size of the headers differs from the declared one
	CodeDirectoryTruncated     = "zip-directory-truncated"      // a central directory header runs past the end of the directory
	CodeDirectorySignature     = "zip-directory-signature"      // something other than a central directory header in the directory
	CodeExtraField             = "zip-extra-field"              // the extra field is not a sequence of (id, size, data) blocks
	CodeDirectoryEntrySize     = "zip-directory-entry-size"     // an entry whose name ends with "/" has a non-zero size
	CodeCommentEncoding        = "zip-comment-encoding"         // the comment of an entry with the UTF-8 flag is not UTF-8
	CodeZip64Inconsistent      = "zip64-inconsistent"           // ZIP64 records missing, misplaced or contradicting the 32-bit ones

	CodeNameEmpty       = "name-empty"             // an entry without a name
	CodeNameNUL         = "name-nul"               // a zero byte in a name
	CodeNameEncoding    = "name-encoding"          // not UTF-8 with the UTF-8 flag, or not ASCII without it
	CodeNameControl     = "name-control-character" // a control character in a name
	CodeNameBackslash   = "name-backslash"         // a backslash in a name
	CodeNameAbsolute    = "name-absolute"          // a leading slash or a drive letter
	CodeNameTraversal   = "name-traversal"         // a "..", "." or empty path segment
	CodeNameDuplicate   = "name-duplicate"         // two entries that cannot both exist in one tree
	CodeNameUnicodePath = "name-unicode-path"      // a Unicode Path extra field that does not give the name of the entry

	CodeLimitEntries       = "limit-entries"
	CodeLimitDirectorySize = "limit-directory-size"
	CodeLimitNameLength    = "limit-name-length"
	CodeLimitCommentLength = "limit-comment-length"
	CodeLimitExtraLength   = "limit-extra-length"

	CodeUnsupportedEncryption       = "unsupported-encryption"        // traditional, strong or AE-x encryption; encrypted directory
	CodeUnsupportedMethod           = "unsupported-method"            // a compression method other than Stored (0) and Deflate (8)
	CodeUnsupportedMultiVolume      = "unsupported-multi-volume"      // a split or spanned archive
	CodeUnsupportedVersion          = "unsupported-version"           // version needed to extract above 4.5
	CodeUnsupportedPatch            = "unsupported-patch"             // patch data
	CodeUnsupportedFlag             = "unsupported-flag"              // a reserved or unused bit of the general purpose flag is set
	CodeUnsupportedDigitalSignature = "unsupported-digital-signature" // a digital signature record in the central directory
	CodeUnsupportedZip64Data        = "unsupported-zip64-data"        // a ZIP64 extensible data sector (or a version 2 record)
)

// Error is an error of the reader about the archive. Errors of the io.ReaderAt the caller supplied are not
// wrapped in it: they are returned as they are.
type Error struct {
	Code string // one of the Code* constants
	// Entry is the index of the entry in the central directory the error is about, or -1 when the error is about
	// the archive as a whole.
	Entry int
	// Other is the index of the second entry involved (for example the earlier of two entries with the same
	// name), or -1.
	Other int
	// Name is the name of the entry as far as it is known when the error is detected; it may be empty and, for
	// name-* codes, it is the rejected name as stored in the archive.
	Name string
	// Offset is the position in the file of the record the error is about.
	Offset int64
	// Message describes the error in English. It is not part of the contract and may change between versions.
	Message string
}

// Error returns the code, the entry (when the error is about one), the offset and the message.
func (e *Error) Error() string {
	s := "zip: " + e.Code
	if e.Entry >= 0 {
		s += " (entry " + strconv.Itoa(e.Entry)
		if e.Name != "" {
			s += " " + strconv.Quote(e.Name)
		}
		s += ")"
	}
	return s + " at offset " + strconv.FormatInt(e.Offset, 10) + ": " + e.Message
}

// archiveError buduje błąd dotyczący archiwum jako całości.
func archiveError(code string, offset int64, message string) *Error {
	return &Error{Code: code, Entry: -1, Other: -1, Offset: offset, Message: message}
}

// entryError buduje błąd dotyczący wpisu o indeksie entry.
func entryError(code string, entry int, name string, offset int64, message string) *Error {
	return &Error{Code: code, Entry: entry, Other: -1, Name: name, Offset: offset, Message: message}
}
