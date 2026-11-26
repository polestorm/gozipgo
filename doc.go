// Package gozipgo reads (and, later, writes) ZIP archives in pure Go, written from the ZIP file format
// specification, for input that is not trusted: every structure is checked against the others, limits are on by
// default, errors have fixed codes, and the same archive always gives the same result.
//
// The code lives in the subpackages; this package holds only the documentation of the module.
package gozipgo
