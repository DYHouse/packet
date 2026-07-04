package nacos

import "errors"

// ErrServiceNameEmpty 表示服务名为空时注册或反注册的哨兵错误。
var ErrServiceNameEmpty = errors.New("service name is empty")
