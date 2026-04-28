# 错误码

!!系统错误码列表，由 `codegen -type=int -doc` 命令生成，不要对此文件做任何更改。

## 功能说明

如果返回结果中存在 `code` 字段，则表示调用 API 接口失败。例如:

```json
{
  "code": 100101,
  "message": "Database error"
}
```

上述返回中 `code` 表示错误码, `message` 表示该错误的具体信息。每个错误同时也对应一个 HTTP 状态码，比如上述错误码对应了 HTTP 状态码 500(Internal Server Error)。

## 错误码列表

系统支持的错误码列表如下:

| Identifier | Code | HTTP Code | Description |
| ---------- | ---- | --------- | ----------- |
| ErrUnknown | 100000 | 500 | Internal server error |
| ErrInvalidArg | 100001 | 400 | Invalid argument |
| ErrUnauthorized | 100002 | 401 | Unauthorized |
| ErrForbidden | 100003 | 403 | Forbidden |
| ErrNotFound | 100004 | 404 | Not found |
| ErrInternal | 100005 | 500 | Internal server error |
| ErrDeadlineExceeded | 100006 | 408 | Deadline exceeded |
| ErrRouteProviderNotAvailable | 110000 | 503 | Route provider is not available in current cluster |
| ErrRouteResourceNotFound | 110001 | 404 | Traffic resource (Ingress or HTTPRoute) not found |
| ErrRouteInvalid | 110002 | 400 | Route rule is invalid |
| ErrRouteApplyFailed | 110003 | 500 | Apply route rules failed |
| ErrRouteAutoDetectFailed | 110004 | 500 | Auto detect route provider failed |

