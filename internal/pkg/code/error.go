package code

type ErrorCode int

const (
	ErrUnknown          ErrorCode = iota + 100000 // ErrUnknown - 500: Internal server error.
	ErrInvalidArg                                 // ErrInvalidArg - 400: Invalid argument.
	ErrUnauthorized                               // ErrUnauthorized - 401: Unauthorized.
	ErrForbidden                                  // ErrForbidden - 403: Forbidden.
	ErrNotFound                                   // ErrNotFound - 404: Not found.
	ErrInternal                                   // ErrInternal - 500: Internal server error.
	ErrDeadlineExceeded                           // ErrDeadlineExceeded - 408: Deadline exceeded.
)
