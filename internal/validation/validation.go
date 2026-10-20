// Package validation holds the input validation error shared by every service. The API
// renders it as 422 with one reason per rejected field.
package validation

import (
	"sort"
	"strings"
)

// Error names each rejected input field and the reason.
type Error struct {
	Fields map[string]string
}

func (e *Error) Error() string {
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return "invalid " + strings.Join(names, ", ")
}

// Check collects field errors; Err returns them as an *Error, or nil when there are none.
type Check map[string]string

// Add records reason for field unless the field already has one.
func (c Check) Add(field, reason string) {
	if _, ok := c[field]; !ok {
		c[field] = reason
	}
}

// Err returns the collected errors, or nil.
func (c Check) Err() error {
	if len(c) == 0 {
		return nil
	}
	return &Error{Fields: c}
}
