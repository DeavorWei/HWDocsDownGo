package logger

import (
	"fmt"
	"runtime/debug"

	"go.uber.org/zap"
)

// SafeGo 启动一个具备 Panic 恢复与调用栈记录的安全 Goroutine
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				Error(fmt.Sprintf("🚨 协程 [%s] 发生未捕获 Panic，已安全拦截", name),
					zap.Any("panic", r),
					zap.String("stack", stack),
				)
			}
		}()
		fn()
	}()
}
