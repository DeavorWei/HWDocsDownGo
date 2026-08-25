package downloader

type TaskStatus int

const (
	StatusQueued      TaskStatus = 0 // 排队中
	StatusDownloading TaskStatus = 1 // 下载中
	StatusCompleted   TaskStatus = 2 // 已完成
	StatusFailed      TaskStatus = 3 // 失败
	StatusPaused      TaskStatus = 4 // 已暂停
	StatusCanceled    TaskStatus = 5 // 已取消
)

func (s TaskStatus) String() string {
	switch s {
	case StatusQueued:
		return "排队中"
	case StatusDownloading:
		return "下载中"
	case StatusCompleted:
		return "已完成"
	case StatusFailed:
		return "失败"
	case StatusPaused:
		return "已暂停"
	case StatusCanceled:
		return "已取消"
	default:
		return "未知"
	}
}

// ProgressEvent 进度广播事件
type ProgressEvent struct {
	TaskID          string     `json:"taskId"`
	DocNID          string     `json:"docNid"`
	DocName         string     `json:"docName"`
	FileName        string     `json:"fileName"`
	SavePath        string     `json:"savePath"`
	TotalBytes      int64      `json:"totalBytes"`
	DownloadedBytes int64      `json:"downloadedBytes"`
	Progress        float64    `json:"progress"`
	SpeedKBps       float64    `json:"speedKbps"`
	SpeedStr        string     `json:"speedStr"`
	Status          TaskStatus `json:"status"`
	StatusStr       string     `json:"statusStr"`
	ErrorMsg        string     `json:"errorMsg"`
}
