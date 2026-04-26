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

// Route 域错误码（v2.6 流量层）
// 编号区段 110000-110099 留给 internal/route 包。
const (
	ErrRouteProviderNotAvailable ErrorCode = iota + 110000 // ErrRouteProviderNotAvailable - 503: Route provider is not available in current cluster.
	ErrRouteResourceNotFound                               // ErrRouteResourceNotFound - 404: Traffic resource (Ingress or HTTPRoute) not found.
	ErrRouteInvalid                                        // ErrRouteInvalid - 400: Route rule is invalid.
	ErrRouteApplyFailed                                    // ErrRouteApplyFailed - 500: Apply route rules failed.
	ErrRouteAutoDetectFailed                               // ErrRouteAutoDetectFailed - 500: Auto detect route provider failed.
)
