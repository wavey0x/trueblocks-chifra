package usage

import (
	"errors"
	"strconv"
	"strings"
)

// Replace accepts a string and an arbitrary number of additional string parameters. It replaces
// {N} in the string with the Nth additional string.
func Replace(msg string, values ...string) string {
	ret := msg
	for index, val := range values {
		rep := "{" + strconv.FormatInt(int64(index), 10) + "}"
		ret = strings.ReplaceAll(ret, rep, val)
	}
	return ret
}

type Error struct {
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// Usage returns an error version of the Replace string
func Usage(msg string, values ...string) error {
	return &Error{Message: Replace(msg, values...)}
}

func Is(err error) bool {
	var usageErr *Error
	return errors.As(err, &usageErr)
}
