package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var staticFiles embed.FS

// GetStaticFS 获取嵌入的静态文件系统
func GetStaticFS() fs.FS {
	return staticFiles
}
