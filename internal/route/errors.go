package route

import (
	"fmt"

	"github.com/Ixecd/kubepivot/internal/code"
)

// Error 包内统一错误类型。
//
// 复用项目级错误码体系（internal/code），同时携带 route 包内的
// 上下文信息（ns / name / detail）便于定位。
//
// 用法：
//
//	return nil, route.NewError(code.ErrRouteResourceNotFound,
//	    "ns=%s name=%s", ns, name)
//
// 调用方判断错误类型：
//
//	var rerr *route.Error
//	if errors.As(err, &rerr) && rerr.Code == code.ErrRouteResourceNotFound {
//	    // 资源不存在的特殊处理
//	}
type Error struct {
	Code    code.ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	desc := code.ErrorCodeString(e.Code)
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", desc, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", desc, e.Message)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// NewError 构造一个 route.Error，不带 cause。
func NewError(c code.ErrorCode, format string, args ...any) *Error {
	return &Error{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
	}
}

// WrapError 构造一个携带底层错误的 route.Error。
//
// 典型用法：包装 kubectl exec 的错误：
//
//	if err := executor.Exec(...); err != nil {
//	    return route.WrapError(code.ErrRouteApplyFailed, err,
//	        "kubectl apply ingress %s/%s", ns, name)
//	}
func WrapError(c code.ErrorCode, cause error, format string, args ...any) *Error {
	return &Error{
		Code:    c,
		Message: fmt.Sprintf(format, args...),
		Cause:   cause,
	}
}
