package system

// FileStats 文件统计信息
type FileStats struct {
	TotalFiles     int   `json:"total_files"`     // 总文件数
	TotalSize      int64 `json:"total_size"`      // 总文件大小（字节）
	TodayUploads   int   `json:"today_uploads"`   // 今日上传数
	CleanableFiles int   `json:"cleanable_files"` // 可清理文件数
	AvatarCount    int   `json:"avatar_count"`    // 头像文件数
	AvgFileSize    int64 `json:"avg_file_size"`   // 平均文件大小
}

// StorageConfig 存储目录配置
type StorageConfig struct {
	BaseDir string
	Dirs    []string
}

// PmacctData pmacct 数据结构
type PmacctData struct {
	Interface string `json:"interface"`
	RxMB      int64  `json:"rx_mb"`
	TxMB      int64  `json:"tx_mb"`
	TotalMB   int64  `json:"total_mb"`
}

const (
	// 默认存储基础目录
	DefaultStorageDir = "storage"

	// 存储子目录
	LogsDir    = "logs"
	UploadsDir = "uploads"
	ExportsDir = "exports"
	ConfigsDir = "configs"
	CertsDir   = "certs"
	CacheDir   = "cache"
	TempDir    = "temp"
	AvatarsDir = "uploads/avatars"
)
