package code

type ErrorCode int

const (
	ErrUnknown          ErrorCode = iota + 100000 // 100000: Unknown.
	ErrInvalidArg                                 // 400001: Invalid arg.
	ErrUnauthorized                               // 401002: Unauthorized.
	ErrForbidden                                  // 403003: Forbidden.
	ErrNotFound                                   // 404004: Not found.
	ErrInternal                                   // 500005: Internal error.
	ErrDeadlineExceeded                           // 408006: Deadline exceeded.
)
