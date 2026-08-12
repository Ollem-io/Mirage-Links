package domain

import "fmt"

// ErrorKind classifies errors so transport adapters can map them without parsing text.
type ErrorKind string

const (
	Validation   ErrorKind = "validation"
	Conflict     ErrorKind = "conflict"
	NotFound     ErrorKind = "not_found"
	Unauthorized ErrorKind = "unauthorized"
)

type Error struct {
	Kind    ErrorKind
	Field   string
	Message string
}

func (e *Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}
func NewValidation(field, message string) error { return &Error{Validation, field, message} }
func NewConflict(message string) error          { return &Error{Conflict, "", message} }
func NewNotFound(message string) error          { return &Error{NotFound, "", message} }
func NewUnauthorized(message string) error      { return &Error{Unauthorized, "", message} }
func IsKind(err error, kind ErrorKind) bool     { e, ok := err.(*Error); return ok && e.Kind == kind }
