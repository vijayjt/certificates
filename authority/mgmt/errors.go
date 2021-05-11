package mgmt

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"github.com/smallstep/certificates/errs"
	"github.com/smallstep/certificates/logging"
)

// ProblemType is the type of the ACME problem.
type ProblemType int

const (
	// ErrorNotFoundType resource not found.
	ErrorNotFoundType ProblemType = iota
	// ErrorAuthorityMismatchType resource Authority ID does not match the
	// context Authority ID.
	ErrorAuthorityMismatchType
	// ErrorDeletedType resource has been deleted.
	ErrorDeletedType
	// ErrorServerInternalType internal server error.
	ErrorServerInternalType
)

// String returns the string representation of the acme problem type,
// fulfilling the Stringer interface.
func (ap ProblemType) String() string {
	switch ap {
	case ErrorNotFoundType:
		return "notFound"
	case ErrorAuthorityMismatchType:
		return "authorityMismatch"
	case ErrorDeletedType:
		return "deleted"
	case ErrorServerInternalType:
		return "internalServerError"
	default:
		return fmt.Sprintf("unsupported error type '%d'", int(ap))
	}
}

type errorMetadata struct {
	details string
	status  int
	typ     string
	String  string
}

var (
	errorServerInternalMetadata = errorMetadata{
		typ:     ErrorServerInternalType.String(),
		details: "the server experienced an internal error",
		status:  500,
	}
	errorMap = map[ProblemType]errorMetadata{
		ErrorNotFoundType: {
			typ:     ErrorNotFoundType.String(),
			details: "resource not found",
			status:  400,
		},
		ErrorAuthorityMismatchType: {
			typ:     ErrorAuthorityMismatchType.String(),
			details: "resource not owned by authority",
			status:  401,
		},
		ErrorDeletedType: {
			typ:     ErrorNotFoundType.String(),
			details: "resource is deleted",
			status:  403,
		},
		ErrorServerInternalType: errorServerInternalMetadata,
	}
)

// Error represents an ACME
type Error struct {
	Type        string        `json:"type"`
	Detail      string        `json:"detail"`
	Subproblems []interface{} `json:"subproblems,omitempty"`
	Identifier  interface{}   `json:"identifier,omitempty"`
	Err         error         `json:"-"`
	Status      int           `json:"-"`
}

// IsType returns true if the error type matches the input type.
func (e *Error) IsType(pt ProblemType) bool {
	return pt.String() == e.Type
}

// NewError creates a new Error type.
func NewError(pt ProblemType, msg string, args ...interface{}) *Error {
	return newError(pt, errors.Errorf(msg, args...))
}

func newError(pt ProblemType, err error) *Error {
	meta, ok := errorMap[pt]
	if !ok {
		meta = errorServerInternalMetadata
		return &Error{
			Type:   meta.typ,
			Detail: meta.details,
			Status: meta.status,
			Err:    err,
		}
	}

	return &Error{
		Type:   meta.typ,
		Detail: meta.details,
		Status: meta.status,
		Err:    err,
	}
}

// NewErrorISE creates a new ErrorServerInternalType Error.
func NewErrorISE(msg string, args ...interface{}) *Error {
	return NewError(ErrorServerInternalType, msg, args...)
}

// WrapError attempts to wrap the internal error.
func WrapError(typ ProblemType, err error, msg string, args ...interface{}) *Error {
	switch e := err.(type) {
	case nil:
		return nil
	case *Error:
		if e.Err == nil {
			e.Err = errors.Errorf(msg+"; "+e.Detail, args...)
		} else {
			e.Err = errors.Wrapf(e.Err, msg, args...)
		}
		return e
	default:
		return newError(typ, errors.Wrapf(err, msg, args...))
	}
}

// WrapErrorISE shortcut to wrap an internal server error type.
func WrapErrorISE(err error, msg string, args ...interface{}) *Error {
	return WrapError(ErrorServerInternalType, err, msg, args...)
}

// StatusCode returns the status code and implements the StatusCoder interface.
func (e *Error) StatusCode() int {
	return e.Status
}

// Error allows AError to implement the error interface.
func (e *Error) Error() string {
	return e.Detail
}

// Cause returns the internal error and implements the Causer interface.
func (e *Error) Cause() error {
	if e.Err == nil {
		return errors.New(e.Detail)
	}
	return e.Err
}

// ToLog implements the EnableLogger interface.
func (e *Error) ToLog() (interface{}, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return nil, WrapErrorISE(err, "error marshaling authority.Error for logging")
	}
	return string(b), nil
}

// WriteError writes to w a JSON representation of the given error.
func WriteError(w http.ResponseWriter, err *Error) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(err.StatusCode())

	// Write errors in the response writer
	if rl, ok := w.(logging.ResponseLogger); ok {
		rl.WithFields(map[string]interface{}{
			"error": err.Err,
		})
		if os.Getenv("STEPDEBUG") == "1" {
			if e, ok := err.Err.(errs.StackTracer); ok {
				rl.WithFields(map[string]interface{}{
					"stack-trace": fmt.Sprintf("%+v", e),
				})
			}
		}
	}

	if err := json.NewEncoder(w).Encode(err); err != nil {
		log.Println(err)
	}
}